package task

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContentDisposition(t *testing.T) {
	require.Equal(t, `attachment; filename="a b.txt"; filename*=UTF-8''a%20b.txt`, contentDisposition("a b.txt"))
	require.Equal(t, `attachment; filename="____.html"; filename*=UTF-8''%D1%84%D0%B0%D0%B9%D0%BB.html`, contentDisposition("файл.html"))
	require.Equal(t, `attachment; filename="x_y_z;.txt"; filename*=UTF-8''x%22y%5Cz%3B.txt`, contentDisposition(`x"y\z;.txt`))
}

func TestCleanFileName(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd":     "passwd",
		`C:\Users\a\отчёт.pdf`: "отчёт.pdf",
		" a\x00b\nc.txt ":      "abc.txt",
		"..":                   "",
		"dir/":                 "",
		"\xff\xfe":             "",
	} {
		require.Equal(t, want, cleanFileName(in), in)
	}
	long := make([]rune, 300)
	for i := range long {
		long[i] = 'я'
	}
	require.Len(t, []rune(cleanFileName(string(long))), maxFileNameRunes)
}

func TestDetectMime(t *testing.T) {
	require.Equal(t, "image/png", detectMime([]byte("\x89PNG\r\n\x1a\n"), "text/html"), "по содержимому")
	require.Equal(t, "application/pdf", detectMime([]byte{0, 1, 2}, "application/pdf; x=1"), "заявленный — если по содержимому не понять")
	require.Equal(t, "application/octet-stream", detectMime([]byte{0, 1, 2}, "мусор"))
	require.Equal(t, "text/html", detectMime([]byte("<html></html>"), "text/plain"), "HTML по содержимому")
	require.Equal(t, "text/csv", detectMime([]byte("a,b\n1,2\n"), "text/csv"), "текст — заявленный text/*")
	require.Equal(t, "text/plain", detectMime([]byte("a,b"), "application/json"), "текст не выдаётся за не-текст")
	require.Equal(t, "application/octet-stream", detectMime(nil, ""), "пустой файл — не text/plain")
}
