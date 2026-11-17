package schedule

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io"
	"time"
)

//go:embed zoneinfo.zip
var timezoneArchive []byte

// Timezone rules are part of this package version, independent of Host files.
func calendarLocation(name string) (*time.Location, error) {
	if name == "UTC" {
		return time.UTC, nil
	}
	if name == "" || name == "Local" {
		return nil, errInvalid
	}
	archive, err := zip.NewReader(bytes.NewReader(timezoneArchive), int64(len(timezoneArchive)))
	if err != nil {
		return nil, err
	}
	for _, file := range archive.File {
		if file.Name != name {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return time.LoadLocationFromTZData(name, data)
	}
	return nil, errInvalid
}
