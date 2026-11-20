package coding

import (
	"bytes"
	"context"
	"crypto/sha1" // Git's object identifier format.
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
)

type DraftEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
}
type DraftTree struct {
	SHA     string
	Entries []DraftEntry
	Blobs   map[string][]byte
}

func objectSHA(kind string, body []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "%s %d%c", kind, len(body), byte(0))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

type gitTreeNode struct {
	files    map[string]DraftEntry
	children map[string]*gitTreeNode
}

func (n *gitTreeNode) add(entry DraftEntry) {
	dir, name, more := strings.Cut(entry.Path, "/")
	if !more {
		if n.files == nil {
			n.files = map[string]DraftEntry{}
		}
		n.files[dir] = entry
		return
	}
	if n.children == nil {
		n.children = map[string]*gitTreeNode{}
	}
	if n.children[dir] == nil {
		n.children[dir] = &gitTreeNode{}
	}
	entry.Path = name
	n.children[dir].add(entry)
}
func (n *gitTreeNode) hash() string {
	keys := []string{}
	for key := range n.files {
		keys = append(keys, key)
	}
	for key := range n.children {
		keys = append(keys, key+"/")
	}
	sort.Strings(keys)
	var raw bytes.Buffer
	for _, key := range keys {
		mode, sha := "", ""
		name := strings.TrimSuffix(key, "/")
		if name != key {
			mode = "40000"
			sha = n.children[name].hash()
		} else {
			e := n.files[name]
			mode = e.Mode
			sha = e.SHA
		}
		fmt.Fprintf(&raw, "%s %s%c", mode, name, byte(0))
		b, _ := hex.DecodeString(sha)
		raw.Write(b)
	}
	return objectSHA("tree", raw.Bytes())
}

// DraftObjects reads only a private immutable candidate and commits regular
// file bytes and executable bits exactly, independent of Git filters or hooks.
func DraftObjects(ctx context.Context, snapshot *CandidateSnapshot) (DraftTree, error) {
	var out DraftTree
	out.Entries = []DraftEntry{}
	out.Blobs = map[string][]byte{}
	root, e := os.OpenRoot(snapshot.Directory)
	if e != nil {
		return out, e
	}
	defer root.Close()
	var files []manifestFile
	digest, e := scanTree(ctx, snapshot.Directory, "", func(f []manifestFile) { files = f }, NativeImportLimits())
	if e != nil || digest != snapshot.SHA256 {
		return out, ErrInvalid
	}
	node := gitTreeNode{}
	for _, f := range files {
		data, e := root.ReadFile(f.Name)
		if e != nil {
			return out, e
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.Hash {
			return out, ErrInvalid
		}
		sha := objectSHA("blob", data)
		mode := "100644"
		if f.Mode&0111 != 0 {
			mode = "100755"
		}
		entry := DraftEntry{Path: f.Name, Mode: mode, Type: "blob", SHA: sha}
		out.Entries = append(out.Entries, entry)
		node.add(entry)
		out.Blobs[sha] = data
	}
	out.SHA = node.hash()
	return out, nil
}
