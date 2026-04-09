package audit

import (
	"io"
	"os"
)

// Verify reads an audit log from r and validates the entire hash chain.
// It returns the number of valid entries and an error describing the
// first chain failure (line number, what was expected, what was found).
//
// A successfully verified log returns (count, nil). An empty stream
// returns (0, nil) — see VerifyFile for the slightly stricter check
// that empty *files* are an error because every real procuracy log
// should at least contain its open anchor.
//
// Verify is the engine that the procuracy verify CLI command calls,
// and the same engine the Writer uses internally on Open. Tampering
// detected here is the operator's signal to investigate; do not
// continue appending to a log that fails verification.
func Verify(r io.Reader) (int, error) {
	count, _, err := verifyAndCount(r)
	return count, err
}

// VerifyFile is a convenience wrapper that opens path and runs Verify.
// In addition to the chain check, VerifyFile treats a zero-byte file
// as an error: a real procuracy log always contains at least the
// open anchor entry, so an empty file means either the log was never
// opened by procuracy or someone truncated it.
func VerifyFile(path string) (int, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if info.Size() == 0 {
		return 0, errEmptyLog
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return Verify(f)
}

// errEmptyLog is sentinel-ish but unexported because callers should
// not need to distinguish empty-file from other errors — both mean
// "this is not a usable procuracy audit log."
var errEmptyLog = &emptyLogError{}

type emptyLogError struct{}

func (*emptyLogError) Error() string {
	return "audit log is empty (expected at least an open anchor entry)"
}
