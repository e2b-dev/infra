package template

import (
	"fmt"
	"os"
)

const (
	MemfileName  = "memfile"
	RootfsName   = "rootfs.ext4"
	SnapfileName = "snapfile"
)

var BucketName = os.Getenv("BUCKET_NAME")

type TemplateFiles struct {
	templateID string
	buildID    string
}

func NewTemplateFiles(templateID, buildID string) *TemplateFiles {
	return &TemplateFiles{
		templateID: templateID,
		buildID:    buildID,
	}
}

func (t *TemplateFiles) BuildDir() string {
	return fmt.Sprintf("%s/%s", t.templateID, t.buildID)
}

func (t *TemplateFiles) MemfilePath() string {
	return fmt.Sprintf("%s/%s", t.BuildDir(), MemfileName)
}

func (t *TemplateFiles) RootfsPath() string {
	return fmt.Sprintf("%s/%s", t.BuildDir(), RootfsName)
}

func (t *TemplateFiles) SnapfilePath() string {
	return fmt.Sprintf("%s/%s", t.BuildDir(), SnapfileName)
}
