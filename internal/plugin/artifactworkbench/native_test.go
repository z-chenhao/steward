package artifactworkbench

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/z-chenhao/assistant/internal/plugin/artifactworkbench/contract"
)

type nativeDescriptor struct {
	Alias string `json:"alias"`
	Name  string `json:"name"`
	Media string `json:"media_type"`
}
type nativePolicyCase struct {
	ID           string                                    `json:"id"`
	Kind         string                                    `json:"kind"`
	Input        json.RawMessage                           `json:"input"`
	Error        string                                    `json:"error"`
	Text         string                                    `json:"text"`
	Matches      []nativeDescriptor                        `json:"matches"`
	Policy       struct{ RetentionDays, CleanupBatch int } `json:"policy"`
	SourceBodies [][]byte                                  `json:"source_bodies"`
	Receipt      string                                    `json:"receipt"`
	Objects      []nativeDescriptor                        `json:"objects"`
	Eligible     []string                                  `json:"eligible"`
}

func originalCases(t *testing.T, name, expected string, count int) []nativePolicyCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != expected {
		t.Fatal("independent native oracle changed")
	}
	var cases []nativePolicyCase
	if json.Unmarshal(raw, &cases) != nil || len(cases) != count {
		t.Fatal("incomplete independent native cases")
	}
	return cases
}

type sourceFixture struct {
	alias, name, media, body string
	created                  int64
}

func nativeSources() []sourceFixture {
	rows := []sourceFixture{{"seed", "seed.txt", "text/plain", "seed", 0}, {"unicode-text", "Évidence报告.md", "text/markdown", "hello 你好🚀\nlast", 1}, {"json-text", "state.json", "application/vnd.fixture+json; charset=utf-8", `{"ok":true}`, 2}, {"binary", "state.bin", "application/octet-stream", string([]byte{0, 255, 1}), 3}, {"invalid-utf8-text", "bad.txt", "text/plain", string([]byte{0xff, 0xfe}), 4}, {"empty-text", "empty.txt", "text/plain", "", 5}, {"percent-name", "100%_done.txt", "text/plain", "literal punctuation", 6}, {"greek-name", "ΣΑΜΟΣ.txt", "text/plain", "greek bytes", 7}}
	for i := 0; i < 55; i++ {
		rows = append(rows, sourceFixture{fmt.Sprintf("newer-%02d", i), fmt.Sprintf("newer-%02d.txt", i), "text/plain", "newer", int64(i + 8)})
	}
	// The native real-Executor oracle already excludes the newer owner-only
	// matching Artifact. The producer is supplied only the authorized universe.
	// Deliberately reorder it: the producer, not source SQL, must rank by time.
	sort.Slice(rows, func(i, j int) bool { return rows[i].alias < rows[j].alias })
	return rows
}
func TestCapturedNativeReadSearchAndKindPolicy(t *testing.T) {
	cases := originalCases(t, "native-read-search.json", "86ff3db1510b5f8f6bca7384e544235eaf0930ba97619d28e5bb71a2a4dc0b53", 39)
	sources := nativeSources()
	byAlias := map[string]sourceFixture{}
	rows := []contract.SearchRow{}
	for _, s := range sources {
		byAlias[s.alias] = s
		rows = append(rows, contract.SearchRow{CreatedAt: s.created, Name: s.name, MediaType: s.media})
	}
	raw := contract.EncodeSearch(rows)
	recipe, err := Recipe(contract.RecipeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			switch c.Kind {
			case "content_kind":
				var input string
				if json.Unmarshal(c.Input, &input) != nil {
					t.Fatal("bad captured MIME")
				}
				media, _, _ := mime.ParseMediaType(input)
				kind := "file"
				for _, allowed := range recipe.MediaKinds {
					if strings.HasPrefix(media, allowed+"/") {
						kind = allowed
						break
					}
				}
				if kind != c.Text {
					t.Fatal(kind, c.Text)
				}
			case "search":
				var input contract.SearchRequest
				if json.Unmarshal(c.Input, &input) != nil {
					t.Fatal("bad captured query")
				}
				input.Count = len(rows)
				plan, err := Select(context.Background(), input, bytes.NewReader(raw))
				if err != nil || plan.ErrorCode != "" {
					t.Fatal(plan, err)
				}
				actual := []string{}
				expected := []string{}
				for _, index := range plan.Indices {
					actual = append(actual, sources[index].alias)
				}
				for _, d := range c.Matches {
					expected = append(expected, d.Alias)
				}
				if !reflect.DeepEqual(actual, expected) {
					t.Fatal(actual, expected)
				}
			case "read":
				var input struct {
					Alias  string `json:"alias"`
					Offset int64  `json:"offset"`
					Limit  int    `json:"limit"`
				}
				if json.Unmarshal(c.Input, &input) != nil {
					t.Fatal("bad captured range")
				}
				source := byAlias[input.Alias]
				plan := Read(contract.ReadRequest{MediaType: source.media, ByteSize: int64(len(source.body)), Offset: input.Offset, Limit: input.Limit})
				if plan.Kind == "binary" {
					if c.Error != "" || plan.Notice != c.Text {
						t.Fatal(plan, c)
					}
					return
				}
				if plan.ErrorCode != "" {
					if plan.ErrorCode != c.Error {
						t.Fatal(plan, c.Error)
					}
					return
				}
				// Assert the selected range against the independently captured real Tool
				// result. UTF-8 validity/exact byte projection are trusted Host invariants;
				// this component test does not claim the future Host adapter is connected.
				data := []byte(source.body)[input.Offset:min(int64(len(source.body)), input.Offset+int64(plan.Limit))]
				if !utf8.Valid(data) {
					if c.Error != "INVALID_ARGUMENT" {
						t.Fatal("native invalid-byte rejection lost")
					}
					return
				}
				var expected struct {
					Text   string `json:"text"`
					Offset int64  `json:"offset"`
					Bytes  int    `json:"bytes"`
					Total  int64  `json:"total_bytes"`
				}
				if c.Error != "" || json.Unmarshal([]byte(c.Text), &expected) != nil || expected.Text != string(data) || expected.Offset != input.Offset || expected.Bytes != len(data) || expected.Total != int64(len(source.body)) {
					t.Fatal("read policy changed native range", plan, c)
				}
			default:
				t.Fatal("unknown captured operation")
			}
		})
	}
}
func TestCapturedNativeOutputRecipeAndCleanupSelection(t *testing.T) {
	cases := originalCases(t, "native-output-cleanup.json", "0c9c98d38861536905741070f44d1eb5b6322561ed0f69d2e342b1bb893783f7", 14)
	recipe, err := Recipe(contract.RecipeRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			switch c.Kind {
			case "defaults":
				if recipe.RetentionDays != c.Policy.RetentionDays {
					t.Fatal(recipe, c.Policy)
				}
			case "output":
				if c.ID == "overflow-one-byte" || c.ID == "unicode-overflow" {
					if len(c.Objects) != 1 || recipe.OverflowName != c.Objects[0].Name || recipe.OverflowMedia != c.Objects[0].Media || recipe.StoredText != c.Receipt {
						t.Fatal(recipe, c)
					}
				}
				// Explicit metadata/ResultJSON are absent from this recipe request: they
				// stay original caller facts. Their actual managed acceptance remains an
				// owning Host integration gate, not this pure policy comparison.
				if recipe.RetentionDays != c.Policy.RetentionDays {
					t.Fatal("creation retention changed", recipe, c.Policy)
				}
			case "cleanup":
				names := []string{"newer-expired", "oldest-expired"}
				rows := []contract.CleanupRow{{ExpiresAt: 3}, {ExpiresAt: 1}}
				if c.ID == "dry-after-first" || c.ID == "apply-second" {
					names = names[:1]
					rows = rows[:1]
				}
				if c.ID == "dry-completed" {
					names = nil
					rows = nil
				}
				plan, err := SelectCleanup(context.Background(), contract.CleanupRequest{Count: len(rows), Batch: c.Policy.CleanupBatch}, bytes.NewReader(contract.EncodeCleanup(rows)))
				if err != nil || plan.ErrorCode != "" {
					t.Fatal(plan, err)
				}
				actual := []string{}
				for _, index := range plan.Indices {
					actual = append(actual, names[index])
				}
				if !slices.Equal(actual, c.Eligible) {
					t.Fatal(actual, c.Eligible)
				}
			default:
				t.Fatal(c.Kind)
			}
		})
	}
	for _, days := range []int{1, 3650, 91} {
		p, err := Recipe(contract.RecipeRequest{RetentionDays: days})
		if err != nil || p.RetentionDays != days {
			t.Fatal("explicit owner retention overridden", p, err)
		}
	}
}
func TestSelectionsRejectPartialTrailingMalformedAndCancelledSource(t *testing.T) {
	rows := []contract.SearchRow{{CreatedAt: 1, Name: "one", MediaType: "text/plain"}}
	raw := contract.EncodeSearch(rows)
	request := contract.SearchRequest{Count: 1, Limit: 1}
	for _, data := range [][]byte{raw[:len(raw)-1], append(append([]byte{}, raw...), 0), raw[:7]} {
		if _, err := Select(context.Background(), request, bytes.NewReader(data)); err == nil {
			t.Fatal("partial/trailing metadata accepted")
		}
	}
	if _, err := Select(context.Background(), contract.SearchRequest{Count: 2, Limit: 1}, bytes.NewReader(raw)); err == nil {
		t.Fatal("false row count accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Select(ctx, request, bytes.NewReader(raw)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := SelectCleanup(ctx, contract.CleanupRequest{Count: 1, Batch: 1}, bytes.NewReader(contract.EncodeCleanup([]contract.CleanupRow{{ExpiresAt: 1}}))); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestSelectionUsesBoundedTopKAndDeterministicTimeTies(t *testing.T) {
	rows := []contract.SearchRow{{CreatedAt: 20, Name: "one"}, {CreatedAt: 100, Name: "two"}, {CreatedAt: 100, Name: "three"}, {CreatedAt: 10, Name: "four"}}
	plan, err := Select(context.Background(), contract.SearchRequest{Count: len(rows), Limit: 2}, bytes.NewReader(contract.EncodeSearch(rows)))
	if err != nil || !reflect.DeepEqual(plan.Indices, []int{1, 2}) {
		t.Fatal(plan, err)
	}
	h := selectionHeap{newest: true}
	for i := 0; i < 100000; i++ {
		h.add(ranked{index: i, stamp: int64(i)}, 50)
		if len(h.rows) > 50 {
			t.Fatal("catalogue-sized selection memory")
		}
	}
	plan = h.plan(50)
	if plan.Indices[0] != 99999 || plan.Indices[49] != 99950 {
		t.Fatal(plan)
	}
	if err := contract.End(bytes.NewReader([]byte{1})); err == nil {
		t.Fatal("trailing source ignored")
	}
	if err := contract.End(bytes.NewReader(nil)); err != nil {
		t.Fatal(err)
	}
}
