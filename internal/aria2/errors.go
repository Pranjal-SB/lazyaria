package aria2

import (
	"fmt"
	"regexp"
	"strconv"
)

// ErrCode is an aria2 exit/error code (manual, "EXIT STATUS").
type ErrCode int

const (
	ErrUnknown           ErrCode = 1 // also what a refused connection reports ("No URI available.")
	ErrTimeout           ErrCode = 2
	ErrResourceNotFound  ErrCode = 3
	ErrNotFoundLimit     ErrCode = 4
	ErrTooSlow           ErrCode = 5
	ErrNetwork           ErrCode = 6
	ErrUnfinished        ErrCode = 7
	ErrResumeUnsupported ErrCode = 8
	ErrDiskFull          ErrCode = 9
	ErrPieceLength       ErrCode = 10
	ErrSameFile          ErrCode = 11
	ErrSameInfoHash      ErrCode = 12
	ErrFileExists        ErrCode = 13
	ErrRenameFailed      ErrCode = 14
	ErrOpenFailed        ErrCode = 15
	ErrCreateFailed      ErrCode = 16
	ErrFileIO            ErrCode = 17
	ErrMkdirFailed       ErrCode = 18
	ErrDNS               ErrCode = 19
	ErrMetalinkParse     ErrCode = 20
	ErrFTPCommand        ErrCode = 21
	ErrHTTPProtocol      ErrCode = 22 // any >=400 response; see HTTPStatus
	ErrTooManyRedirects  ErrCode = 23
	ErrAuthFailed        ErrCode = 24
	ErrBencodeParse      ErrCode = 25
	ErrTorrentCorrupt    ErrCode = 26
	ErrBadMagnet         ErrCode = 27
	ErrBadOption         ErrCode = 28
	ErrServerOverloaded  ErrCode = 29
	ErrRPCParse          ErrCode = 30
	ErrReserved          ErrCode = 31
	ErrChecksum          ErrCode = 32
)

var errText = map[ErrCode]string{
	ErrUnknown:           "unknown error",
	ErrTimeout:           "timed out",
	ErrResourceNotFound:  "resource not found",
	ErrNotFoundLimit:     "too many not-found responses",
	ErrTooSlow:           "download speed too slow",
	ErrNetwork:           "network problem",
	ErrUnfinished:        "unfinished at shutdown",
	ErrResumeUnsupported: "server cannot resume",
	ErrDiskFull:          "not enough disk space",
	ErrPieceLength:       "piece length differs from the control file",
	ErrSameFile:          "same file is already downloading",
	ErrSameInfoHash:      "same info hash is already downloading",
	ErrFileExists:        "file already exists",
	ErrRenameFailed:      "renaming the file failed",
	ErrOpenFailed:        "could not open existing file",
	ErrCreateFailed:      "could not create or truncate file",
	ErrFileIO:            "file I/O error",
	ErrMkdirFailed:       "could not create directory",
	ErrDNS:               "name resolution failed",
	ErrMetalinkParse:     "could not parse metalink",
	ErrFTPCommand:        "FTP command failed",
	ErrHTTPProtocol:      "unexpected HTTP response",
	ErrTooManyRedirects:  "too many redirects",
	ErrAuthFailed:        "authorization failed",
	ErrBencodeParse:      "could not parse bencoded file",
	ErrTorrentCorrupt:    "torrent file is corrupt",
	ErrBadMagnet:         "bad magnet URI",
	ErrBadOption:         "bad option",
	ErrServerOverloaded:  "server is overloaded",
	ErrRPCParse:          "could not parse the JSON-RPC request",
	ErrReserved:          "reserved code",
	ErrChecksum:          "checksum validation failed",
}

func (c ErrCode) String() string {
	if s, ok := errText[c]; ok {
		return s
	}
	return fmt.Sprintf("aria2 error %d", int(c))
}

// RPCError is an error object returned by aria2 for a request.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("aria2 rpc error %d: %s", e.Code, e.Message) }

var statusRe = regexp.MustCompile(`status=(\d{3})`)

// HTTPStatus pulls NNN out of "...status=NNN"; 0 when absent.
func HTTPStatus(errorMessage string) int {
	m := statusRe.FindStringSubmatch(errorMessage)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
