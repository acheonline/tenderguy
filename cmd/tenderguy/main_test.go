package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tenderguy/internal/scanner"
)

const pdfFixture = "%PDF-1.7\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"

func writeFixture(t *testing.T, path string, body []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("создание каталога %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("запись %s: %v", path, err)
	}
}

// packageRoot возвращает репозиторий целиком: годится как «не пустой, но без документов» вход.
func sampleDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "Заявка", "22выписка.pdf"), []byte(pdfFixture))
	writeFixture(t, filepath.Join(dir, "Заявка", "1Опись.docx"), []byte("PK\x03\x04 мусор вместо docx"))
	writeFixture(t, filepath.Join(dir, "Заявка", "памятка.txt"), []byte("текст"))
	return dir
}

func TestRun(t *testing.T) {
	sample := sampleDir(t)
	empty := t.TempDir()
	inside := filepath.Join(sample, "Заявка", "22выписка.pdf")
	linkDir := t.TempDir()
	if err := os.Symlink(inside, filepath.Join(linkDir, "ссылка.pdf")); err != nil {
		t.Skipf("среда не поддерживает символьные ссылки: %v", err)
	}

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantOut    []string
		wantErrOut []string
		noOut      bool
	}{
		{
			name:       "не задан --input-dir",
			args:       nil,
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан каталог с документами", "--input-dir", "Использование"},
			noOut:      true,
		},
		{
			name:       "неизвестный формат",
			args:       []string{"--input-dir", sample, "--format", "xml"},
			wantExit:   exitUsage,
			wantErrOut: []string{`неизвестный --format "xml"`},
			noOut:      true,
		},
		{
			name:       "позиционный аргумент не принимается вместо флага",
			args:       []string{"--input-dir", sample, "/tmp"},
			wantExit:   exitUsage,
			wantErrOut: []string{"лишние аргументы"},
			noOut:      true,
		},
		{
			name:       "каталог не найден",
			args:       []string{"--input-dir", filepath.Join(sample, "нет-такого-каталога")},
			wantExit:   exitBadPath,
			wantErrOut: []string{"каталог не найден"},
			noOut:      true,
		},
		{
			name:       "передан файл",
			args:       []string{"--input-dir", inside},
			wantExit:   exitBadPath,
			wantErrOut: []string{"файл, а не каталог"},
			noOut:      true,
		},
		{
			name:     "текстовый отчёт по каталогу",
			args:     []string{"--input-dir", sample},
			wantExit: exitOK,
			wantOut: []string{
				"Каталог: " + sample,
				"Файлов найдено: 3",
				"поддерживается парсерами P1: 1",
				"Документы для разбора",
				"Прочие файлы",
				"pdf",
				"Заявка/22выписка.pdf",
				"archive",
				"Расхождение расширения и содержимого",
			},
		},
		{
			name:     "json-отчёт",
			args:     []string{"--input-dir", sample, "--format", "json"},
			wantExit: exitOK,
			wantOut:  []string{`"root":`, `"rel_path": "Заявка/22выписка.pdf"`, `"supported": 1`},
		},
		{
			name:     "пустой каталог — подсказка, но не ошибка",
			args:     []string{"--input-dir", empty},
			wantExit: exitOK,
			wantOut:  []string{"Внимание: в каталоге не найдено ни одного файла"},
		},
		{
			name:       "проблемный файл даёт код 1",
			args:       []string{"--input-dir", linkDir},
			wantExit:   exitProblems,
			wantErrOut: []string{"замечаний 1"},
			wantOut:    []string{"ссылка.pdf"},
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

func TestRunJSONReportDecodes(t *testing.T) {
	sample := sampleDir(t)

	var out, errOut bytes.Buffer
	if code := run([]string{"--input-dir", sample, "--format", "json"}, &out, &errOut); code != exitOK {
		t.Fatalf("код выхода = %d, stderr: %s", code, errOut.String())
	}

	var res scanner.Result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("json не разбирается: %v\n%s", err, out.String())
	}
	if res.Total != 3 || res.Supported != 1 {
		t.Errorf("Total/Supported = %d/%d, хотели 3/1", res.Total, res.Supported)
	}
	if res.Counts[scanner.KindPdf] != 1 {
		t.Errorf("Counts[pdf] = %d, хотели 1", res.Counts[scanner.KindPdf])
	}
	for _, e := range res.Entries {
		if !filepath.IsAbs(e.Path) {
			t.Errorf("Path = %q, хотели абсолютный путь", e.Path)
		}
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{0, "0 Б"},
		{999, "999 Б"},
		{1024, "1.0 КиБ"},
		{1536, "1.5 КиБ"},
		{1024 * 1024, "1.0 МиБ"},
		{374 * 1024, "374.0 КиБ"},
	}
	for _, tc := range tests {
		if got := humanSize(tc.size); got != tc.want {
			t.Errorf("humanSize(%d) = %q, хотели %q", tc.size, got, tc.want)
		}
	}
}

func TestUsageMentionsInputDirFlag(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"--input-dir", "Коды выхода", "text, json или markdown"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}
