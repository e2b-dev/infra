package middleware

import (
	"os"
	"time"
)

// Request is the interface for all typed operation requests.
type Request interface {
	Op() string
}

// File operations

type FileWriteRequest struct {
	Data []byte
}

func (r FileWriteRequest) Op() string { return "File.Write" }

type FileReadRequest struct {
	Buffer []byte
}

func (r FileReadRequest) Op() string { return "File.Read" }

type FileReadAtRequest struct {
	Buffer []byte
	Offset int64
}

func (r FileReadAtRequest) Op() string { return "File.ReadAt" }

type FileSeekRequest struct {
	Offset int64
	Whence int
}

func (r FileSeekRequest) Op() string { return "File.Seek" }

type FileCloseRequest struct{}

func (r FileCloseRequest) Op() string { return "File.Close" }

type FileLockRequest struct{}

func (r FileLockRequest) Op() string { return "File.Lock" }

type FileUnlockRequest struct{}

func (r FileUnlockRequest) Op() string { return "File.Unlock" }

type FileTruncateRequest struct {
	Size int64
}

func (r FileTruncateRequest) Op() string { return "File.Truncate" }

// Filesystem operations

type FSCreateRequest struct {
	Filename string
}

func (r FSCreateRequest) Op() string { return "FS.Create" }

type FSOpenRequest struct {
	Filename string
}

func (r FSOpenRequest) Op() string { return "FS.Open" }

type FSOpenFileRequest struct {
	Filename string
	Flag     int
	Perm     os.FileMode
}

func (r FSOpenFileRequest) Op() string { return "FS.OpenFile" }

type FSStatRequest struct {
	Filename string
}

func (r FSStatRequest) Op() string { return "FS.Stat" }

type FSRenameRequest struct {
	OldPath string
	NewPath string
}

func (r FSRenameRequest) Op() string { return "FS.Rename" }

type FSRemoveRequest struct {
	Filename string
}

func (r FSRemoveRequest) Op() string { return "FS.Remove" }

type FSTempFileRequest struct {
	Dir    string
	Prefix string
}

func (r FSTempFileRequest) Op() string { return "FS.TempFile" }

type FSReadDirRequest struct {
	Path string
}

func (r FSReadDirRequest) Op() string { return "FS.ReadDir" }

type FSMkdirAllRequest struct {
	Filename string
	Perm     os.FileMode
}

func (r FSMkdirAllRequest) Op() string { return "FS.MkdirAll" }

type FSLstatRequest struct {
	Filename string
}

func (r FSLstatRequest) Op() string { return "FS.Lstat" }

type FSSymlinkRequest struct {
	Target string
	Link   string
}

func (r FSSymlinkRequest) Op() string { return "FS.Symlink" }

type FSReadlinkRequest struct {
	Link string
}

func (r FSReadlinkRequest) Op() string { return "FS.Readlink" }

type FSChrootRequest struct {
	Path string
}

func (r FSChrootRequest) Op() string { return "FS.Chroot" }

// Change operations

type ChangeChmodRequest struct {
	Name string
	Mode os.FileMode
}

func (r ChangeChmodRequest) Op() string { return "Change.Chmod" }

type ChangeLchownRequest struct {
	Name string
	UID  int
	GID  int
}

func (r ChangeLchownRequest) Op() string { return "Change.Lchown" }

type ChangeChownRequest struct {
	Name string
	UID  int
	GID  int
}

func (r ChangeChownRequest) Op() string { return "Change.Chown" }

type ChangeChtimesRequest struct {
	Name  string
	ATime time.Time
	MTime time.Time
}

func (r ChangeChtimesRequest) Op() string { return "Change.Chtimes" }

// Handler operations

type HandlerMountRequest struct {
	RemoteAddr string
	Dirpath    string
}

func (r HandlerMountRequest) Op() string { return "Handler.Mount" }

type HandlerChangeRequest struct{}

func (r HandlerChangeRequest) Op() string { return "Handler.Change" }

type HandlerFSStatRequest struct{}

func (r HandlerFSStatRequest) Op() string { return "Handler.FSStat" }

type HandlerToHandleRequest struct {
	Path []string
}

func (r HandlerToHandleRequest) Op() string { return "Handler.ToHandle" }

type HandlerFromHandleRequest struct{}

func (r HandlerFromHandleRequest) Op() string { return "Handler.FromHandle" }

type HandlerInvalidateHandleRequest struct{}

func (r HandlerInvalidateHandleRequest) Op() string { return "Handler.InvalidateHandle" }
