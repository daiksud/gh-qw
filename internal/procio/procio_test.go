package procio

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// fileProviderStub implements io.Writer and FileProvider for testing
// composition, such as a mutex-guarded writer wrapping the real standard
// stream.
type fileProviderStub struct {
	file *os.File
}

func (s fileProviderStub) Write(p []byte) (int, error) {
	return len(p), nil
}

func (s fileProviderStub) PassthroughFile() *os.File {
	return s.file
}

func TestPassthroughFileReturnsTheFileItself(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "procio-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer file.Close()

	got := PassthroughFile(file)
	if got != file {
		t.Fatalf("PassthroughFile() = %#v, want the same *os.File %#v", got, file)
	}
}

func TestPassthroughFileDelegatesToFileProvider(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "procio-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer file.Close()

	provider := fileProviderStub{file: file}
	got := PassthroughFile(provider)
	if got != file {
		t.Fatalf("PassthroughFile() = %#v, want the wrapped *os.File %#v", got, file)
	}
}

func TestPassthroughFileReturnsNilForAFileProviderWithNoFile(t *testing.T) {
	provider := fileProviderStub{file: nil}
	if got := PassthroughFile(provider); got != nil {
		t.Fatalf("PassthroughFile() = %#v, want nil", got)
	}
}

func TestPassthroughFileReturnsNilForABuffer(t *testing.T) {
	var buffer bytes.Buffer
	if got := PassthroughFile(&buffer); got != nil {
		t.Fatalf("PassthroughFile() = %#v, want nil", got)
	}
}

func TestPassthroughFileReturnsNilForIoDiscard(t *testing.T) {
	if got := PassthroughFile(io.Discard); got != nil {
		t.Fatalf("PassthroughFile() = %#v, want nil", got)
	}
}

func TestPassthroughFileReturnsNilForMultiWriter(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "procio-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer file.Close()

	var buffer bytes.Buffer
	multi := io.MultiWriter(file, &buffer)
	if got := PassthroughFile(multi); got != nil {
		t.Fatalf("PassthroughFile() = %#v, want nil (an io.MultiWriter is not itself an *os.File)", got)
	}
}

func TestPassthroughFileReturnsNilForNil(t *testing.T) {
	if got := PassthroughFile(nil); got != nil {
		t.Fatalf("PassthroughFile() = %#v, want nil", got)
	}
}

func TestPassthroughFileReturnsNilForATypedNilFile(t *testing.T) {
	var file *os.File
	if got := PassthroughFile(file); got != nil {
		t.Fatalf("PassthroughFile() = %#v, want nil", got)
	}
}
