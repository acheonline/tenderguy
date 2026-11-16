package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePdfFile кладёт декодированную фикстуру в каталог и возвращает путь.
func writePdfFile(t *testing.T, dir, name, b64 string) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("фикстура %s не декодируется: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("запись %s: %v", path, err)
	}
	return path
}

func TestRunParsePdf(t *testing.T) {
	good := writePdfFile(t, t.TempDir(), "doc.pdf", cliPdfTextFixture)

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantOut    []string
		wantErrOut []string
		noOut      bool
	}{
		{
			name:     "текстовый слой, текстовый отчёт",
			args:     []string{"--command", "parse-pdf", "--file", good},
			wantExit: exitOK,
			wantOut: []string{
				"Разобрано PDF: 1 — без потерь 1, с потерями 0, с ошибками 0",
				"Страниц 1 (текстовый слой 1, OCR 0)",
				"без потерь",
			},
		},
		{
			name:     "с текстом",
			args:     []string{"--command", "parse-pdf", "--file", good, "--with-text"},
			wantExit: exitOK,
			wantOut:  []string{"--- текст ---", "CLI pdf fixture alpha bravo charlie"},
		},
		{
			name:       "битый pdf — потери, код 1",
			args:       []string{"--command", "parse-pdf", "--file", writePdfFile(t, filepath.Dir(good), "битый.pdf", "UEsDBAAAAAAAAABuZSBwZGY=")},
			wantExit:   exitProblems,
			wantOut:    []string{"Ошибки разбора:"},
			wantErrOut: []string{"с ошибками 1"},
		},
		{
			name:     "каталог без pdf",
			args:     []string{"--command", "parse-pdf", "--input-dir", t.TempDir()},
			wantExit: exitOK,
			wantOut:  []string{"В нём нет .pdf — разбирать нечего"},
		},
		{
			name:       "не задан вход",
			args:       []string{"--command", "parse-pdf"},
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан вход"},
			noOut:      true,
		},
		{
			name:       "file и input-dir вместе",
			args:       []string{"--command", "parse-pdf", "--file", good, "--input-dir", filepath.Dir(good)},
			wantExit:   exitUsage,
			wantErrOut: []string{"выберите что-то одно"},
			noOut:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			got := run(tc.args, &out, &errOut)
			if got != tc.wantExit {
				t.Errorf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", got, tc.wantExit, out.String(), errOut.String())
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("в stdout нет подстроки %q\n%s", want, out.String())
				}
			}
			for _, want := range tc.wantErrOut {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("в stderr нет подстроки %q\n%s", want, errOut.String())
				}
			}
			if tc.noOut && out.Len() > 0 {
				t.Errorf("stdout должен быть пуст, получили\n%s", out.String())
			}
		})
	}
}

func TestRunParsePdfJSONDecodes(t *testing.T) {
	good := writePdfFile(t, t.TempDir(), "doc.pdf", cliPdfTextFixture)

	var out, errOut bytes.Buffer
	if code := run([]string{"--command", "parse-pdf", "--file", good, "--format", "json"}, &out, &errOut); code != exitOK {
		t.Fatalf("код выхода = %d, stderr: %s", code, errOut.String())
	}

	var rep struct {
		Documents []struct {
			Name   string `json:"name"`
			Format string `json:"format"`
			Pages  []struct {
				Index  int    `json:"index"`
				Method string `json:"method"`
			} `json:"pages"`
			Complete bool   `json:"complete"`
			Content  string `json:"content"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("json не разбирается: %v\n%s", err, out.String())
	}
	if len(rep.Documents) != 1 {
		t.Fatalf("документов %d, хотели 1", len(rep.Documents))
	}
	doc := rep.Documents[0]
	if doc.Name != "doc.pdf" || doc.Format != "pdf" {
		t.Errorf("Name/Format = %q/%q", doc.Name, doc.Format)
	}
	if len(doc.Pages) != 1 || doc.Pages[0].Method != "text" {
		t.Errorf("pages = %+v", doc.Pages)
	}
	if !doc.Complete {
		t.Error("complete = false, хотели true")
	}
	if doc.Content != "" {
		t.Errorf("без --with-text в json не должно быть поля content: %q", doc.Content)
	}
}

func TestUsageMentionsParsePdf(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"parse-pdf", "--file", "--with-text", "Коды выхода"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}

// cliPdfTextFixture — компактный PDF с текстовым слоем для тестов CLI:
//
//	"CLI pdf fixture alpha bravo charlie"

const cliPdfTextFixture = "" +
	"JVBERi0xLjQKJeLjz9MKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2JqCjIgMCBvYmoK" +
	"PDwgL1R5cGUgL1BhZ2VzIC9LaWRzIFszIDAgUl0gL0NvdW50IDEgPj4KZW5kb2JqCjMgMCBvYmoKPDwgL1R5cGUgL1BhZ2Ug" +
	"L1BhcmVudCAyIDAgUiAvTWVkaWFCb3ggWzAgMCA2MTIgNzkyXSAvUmVzb3VyY2VzIDw8IC9Gb250IDw8IC9GMSA1IDAgUiA+" +
	"PiA+PiAvQ29udGVudHMgNCAwIFIgPj4KZW5kb2JqCjQgMCBvYmoKPDwgL0xlbmd0aCA2NiA+PgpzdHJlYW0KQlQgL0YxIDI0" +
	"IFRmIDcyIDcwMCBUZCAoQ0xJIHBkZiBmaXh0dXJlIGFscGhhIGJyYXZvIGNoYXJsaWUpIFRqIEVUCmVuZHN0cmVhbQplbmRv" +
	"YmoKNSAwIG9iago8PCAvVHlwZSAvRm9udCAvU3VidHlwZSAvVHlwZTEgL0Jhc2VGb250IC9IZWx2ZXRpY2EgL0VuY29kaW5n" +
	"IC9XaW5BbnNpRW5jb2RpbmcgPj4KZW5kb2JqCnhyZWYKMCA2CjAwMDAwMDAwMDAgNjU1MzUgZiAKMDAwMDAwMDAxNSAwMDAw" +
	"MCBuIAowMDAwMDAwMDY0IDAwMDAwIG4gCjAwMDAwMDAxMjEgMDAwMDAgbiAKMDAwMDAwMDI0NyAwMDAwMCBuIAowMDAwMDAw" +
	"MzYzIDAwMDAwIG4gCnRyYWlsZXIKPDwgL1NpemUgNiAvUm9vdCAxIDAgUiA+PgpzdGFydHhyZWYKNDYwCiUlRU9GCg=="
