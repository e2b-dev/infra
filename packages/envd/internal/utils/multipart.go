package utils

import (
	"errors"
	"mime"
	"mime/multipart"
)

// CustomPart is a wrapper around multipart.Part that overloads the FileName method
type CustomPart struct {
	*multipart.Part
}

// FileName returns the filename parameter of the Part's Content-Disposition header.
// This method overrides the original FileName method to return the full filename
// without using filepath.Base.
func (p *CustomPart) FileName() (string, error) {
	dispositionParams, err := p.parseContentDisposition()
	if err != nil {
		return "", err
	}
	filename, ok := dispositionParams["filename"]
	if !ok {
		return "", errors.New("filename not found in Content-Disposition header")
	}
	return filename, nil
}

func (p *CustomPart) parseContentDisposition() (map[string]string, error) {
	v := p.Header.Get("Content-Disposition")
	_, dispositionParams, err := mime.ParseMediaType(v)
	if err != nil {
		return nil, err
	}
	return dispositionParams, nil
}

// NewCustomPart creates a new CustomPart from a multipart.Part
func NewCustomPart(part *multipart.Part) *CustomPart {
	return &CustomPart{Part: part}
}
