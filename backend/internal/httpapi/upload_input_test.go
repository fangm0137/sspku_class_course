package httpapi

import (
	"bytes"
	"campusclaw/backend/fixtures"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testUploadPart struct {
	field, name string
	body        []byte
}

func uploadRequest(parts ...testUploadPart) *http.Request {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, part := range parts {
		file, _ := writer.CreateFormFile(part.field, part.name)
		file.Write(part.body)
	}
	writer.WriteField("class_id", "999")
	writer.WriteField("role", "teacher")
	writer.Close()
	r := httptest.NewRequest("POST", "http://localhost/api/materials?class_id=999", bytes.NewReader(body.Bytes()))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	return r
}
func TestUploadInput(t *testing.T) {
	for _, name := range []string{"class-a-intro.txt", "class-a-notes.md"} {
		t.Run(name, func(t *testing.T) {
			content, err := fixtures.Files.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			got, status := parseUpload(httptest.NewRecorder(), uploadRequest(testUploadPart{"file", name, content}))
			if status != 0 || got.text != string(content) || !bytes.Equal(got.body, content) {
				t.Fatal("sample not preserved")
			}
		})
	}
	cases := []struct {
		name, filename string
		body           []byte
		status         int
	}{
		{"limit", "a.txt", bytes.Repeat([]byte("a"), maxMaterial), 0},
		{"too-large", "a.txt", bytes.Repeat([]byte("a"), maxMaterial+1), 413},
		{"bom", "a.MD", []byte("\xef\xbb\xbf# notes"), 0},
		{"blank", "a.txt", []byte(" \r\n\t　"), 400},
		{"empty", "a.txt", nil, 400},
		{"bom-only", "a.txt", []byte("\xef\xbb\xbf"), 400},
		{"invalid-utf8", "a.txt", []byte{0xff}, 400},
		{"extension", "a.pdf", []byte("text"), 400},
		{"long-name", strings.Repeat("a", 252) + ".txt", []byte("text"), 400},
		{"unix-path", "../../outside.txt", []byte("<script>alert(1)</script>"), 0},
		{"windows-path", `C:\outside\notes.md`, []byte("# text"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, status := parseUpload(httptest.NewRecorder(), uploadRequest(testUploadPart{"file", tc.filename, tc.body}))
			if status != tc.status {
				t.Fatalf("status=%d expected=%d", status, tc.status)
			}
			if status == 0 {
				if strings.ContainsAny(got.name, `/\`) || !bytes.Equal(got.body, tc.body) {
					t.Fatal("unsafe name or changed original")
				}
				if tc.name == "bom" && got.text != "# notes" {
					t.Fatal("BOM not removed from text")
				}
			}
		})
	}
	for _, parts := range [][]testUploadPart{nil, {{"other", "a.txt", []byte("a")}}, {{"file", "a.txt", []byte("a")}, {"file", "b.txt", []byte("b")}}} {
		if _, status := parseUpload(httptest.NewRecorder(), uploadRequest(parts...)); status != 400 {
			t.Fatal("missing/wrong/multiple files accepted")
		}
	}
	malformed := httptest.NewRequest("POST", "/", strings.NewReader("broken"))
	malformed.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	if _, s := parseUpload(httptest.NewRecorder(), malformed); s != 400 {
		t.Fatal("malformed accepted")
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("ignored", strings.Repeat("a", maxMaterial+64*1024+1))
	writer.Close()
	r := httptest.NewRequest("POST", "/", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if _, s := parseUpload(httptest.NewRecorder(), r); s != 413 {
		t.Fatal("total request limit missing")
	}
	trailing := uploadRequest(testUploadPart{"file", "a.txt", []byte("valid")})
	prefix, _ := io.ReadAll(trailing.Body)
	trailing.Body = io.NopCloser(bytes.NewReader(append(prefix, bytes.Repeat([]byte("x"), maxMaterial+64*1024)...)))
	if _, s := parseUpload(httptest.NewRecorder(), trailing); s != 413 {
		t.Fatal("trailing bytes bypass request limit")
	}
}
