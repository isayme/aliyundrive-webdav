package adrive

import (
	"io/fs"
	"time"
)

var _ fs.FileInfo = &FileInfo{}

type FileInfo struct {
	FileName     string    `json:"name"`
	FileSize     int64     `json:"size"`
	UpdatedAt    time.Time `json:"updated_at"`
	ContentHash  string    `json:"content_hash"`
	Type         string    `json:"type"`
	DriveId      string    `json:"drive_id"`
	FileId       string    `json:"file_id"`
	ParentFileId string    `json:"parent_file_id"`
}

func (f *FileInfo) Name() string {
	return f.FileName
}

func (f *FileInfo) Size() int64 {
	return f.FileSize
}

func (f *FileInfo) Mode() fs.FileMode {
	var mode fs.FileMode = 0660
	if f.IsDir() {
		mode = mode | fs.ModeDir
	}

	return mode
}

func (f *FileInfo) ModTime() time.Time {
	return f.UpdatedAt
}

func (f *FileInfo) IsDir() bool {
	return f.Type == FILE_TYPE_FOLDER
}

func (f *FileInfo) Sys() any {
	return nil
}
