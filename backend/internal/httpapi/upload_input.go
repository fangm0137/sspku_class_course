package httpapi

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"
)

const maxMaterial = 5 * 1024 * 1024

type uploadInput struct {
	name string
	body []byte
	text string
}

func parseUpload(w http.ResponseWriter, r *http.Request) (uploadInput, int) {
	var result uploadInput
	r.Body = http.MaxBytesReader(w, r.Body, maxMaterial+64*1024)
	reader, err := r.MultipartReader()
	if err != nil {
		return result, 400
	}
	count := 0
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, uploadReadStatus(err)
		}
		_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		if err != nil {
			return result, 400
		}
		filename, isFile := params["filename"]
		if !isFile {
			if _, err = io.Copy(io.Discard, part); err != nil {
				return result, uploadReadStatus(err)
			}
			continue
		}
		count++
		if count != 1 || part.FormName() != "file" || filename == "" {
			return result, 400
		}
		result.name = displayName(filename)
		extension := strings.ToLower(path.Ext(result.name))
		if !utf8.ValidString(result.name) || utf8.RuneCountInString(result.name) > 255 || (extension != ".txt" && extension != ".md") {
			return result, 400
		}
		result.body, err = io.ReadAll(io.LimitReader(part, maxMaterial+1))
		if err != nil {
			return result, uploadReadStatus(err)
		}
		if len(result.body) > maxMaterial {
			return result, 413
		}
	}
	if count != 1 {
		return result, 400
	}
	if _, err = io.Copy(io.Discard, r.Body); err != nil {
		return result, uploadReadStatus(err)
	}
	text := bytes.TrimPrefix(result.body, []byte{0xef, 0xbb, 0xbf})
	if !utf8.Valid(text) || strings.TrimSpace(string(text)) == "" {
		return result, 400
	}
	result.text = string(text)
	return result, 0
}
func uploadReadStatus(err error) int {
	var large *http.MaxBytesError
	if errors.As(err, &large) {
		return 413
	}
	return 400
}
