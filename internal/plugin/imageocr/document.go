package imageocr

import (
	"bufio"
	"errors"
	"github.com/z-chenhao/assistant/internal/plugin/imageocr/contract"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrUnavailable = errors.New("invalid OCR engine output")

// Tesseract TSV is line-delimited, with the final text column unquoted. Text
// remains untrusted content; numeric fields cannot create out-of-image boxes.
func parseTSV(raw []byte, width, height int, languages []string) (contract.Document, error) {
	d := contract.Document{Method: "ocr", Languages: append([]string(nil), languages...), Width: width, Height: height, Words: []contract.Word{}}
	s := bufio.NewScanner(strings.NewReader(string(raw)))
	s.Buffer(make([]byte, 4096), 64<<10)
	if !s.Scan() || s.Text() != "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext" {
		return d, ErrUnavailable
	}
	var text strings.Builder
	line := ""
	for s.Scan() {
		cols := strings.SplitN(s.Text(), "\t", 12)
		if len(cols) != 12 {
			return d, ErrUnavailable
		}
		if cols[0] != "5" || strings.TrimSpace(cols[11]) == "" {
			continue
		}
		w := contract.Word{Text: cols[11]}
		if !utf8.ValidString(w.Text) {
			return d, ErrUnavailable
		}
		values := []*int{&w.X, &w.Y, &w.Width, &w.Height}
		for i, v := range values {
			n, err := strconv.Atoi(cols[6+i])
			if err != nil || n < 0 {
				return d, ErrUnavailable
			}
			*v = n
		}
		if w.Width < 1 || w.Height < 1 || w.X > width-w.Width || w.Y > height-w.Height {
			return d, ErrUnavailable
		}
		confidence, err := strconv.ParseFloat(cols[10], 64)
		if err != nil || math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 100 {
			return d, ErrUnavailable
		}
		w.Confidence = confidence
		if len(d.Words) >= 4096 || text.Len()+len(w.Text)+1 > 128<<10 {
			d.Truncated = true
			break
		}
		current := strings.Join(cols[1:5], ":")
		if text.Len() > 0 {
			if current != line {
				text.WriteByte('\n')
			} else {
				text.WriteByte(' ')
			}
		}
		text.WriteString(w.Text)
		line = current
		d.Words = append(d.Words, w)
	}
	if s.Err() != nil {
		return d, ErrUnavailable
	}
	d.Text = text.String()
	return d, nil
}
