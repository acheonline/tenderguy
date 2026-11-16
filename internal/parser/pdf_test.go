package parser

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// writePdfFixture декодирует base64-фикстуру в файл тестового каталога.
func writePdfFixture(t *testing.T, dir, name, b64 string) string {
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

// sharedTextRatio — доля непространственных символов эталона, дошедших до
// извлечения (регистр не учитываем: OCR его портит на редких глифах). По ней
// проверяем критерий P1.4 «извлечено ≥80 % символов».
func sharedTextRatio(want, got string) float64 {
	remaining := []rune(strings.ToLower(got))
	total, found := 0, 0
	for _, r := range strings.ToLower(want) {
		if unicode.IsSpace(r) {
			continue
		}
		total++
		for i, g := range remaining {
			if g == r {
				found++
				remaining = append(remaining[:i], remaining[i+1:]...)
				break
			}
		}
	}
	if total == 0 {
		return 0
	}
	return float64(found) / float64(total)
}

func TestParsePdfTextLayer(t *testing.T) {
	path := writePdfFixture(t, t.TempDir(), "text.pdf", pdfTextFixture)
	const want = "Tenderguy P1.4 fixture: text layer extraction check."

	doc, err := ParsePdf(path)
	if err != nil {
		t.Fatalf("ParsePdf: %v", err)
	}
	if doc.Format != FormatPdf || doc.Name != "text.pdf" {
		t.Errorf("Format/Name = %q/%q", doc.Format, doc.Name)
	}
	if len(doc.Pages) != 1 {
		t.Fatalf("страниц %d, хотели 1", len(doc.Pages))
	}
	page := doc.Pages[0]
	if page.Method != PdfMethodText {
		t.Errorf("метод страницы = %q, хотели текстовый слой", page.Method)
	}
	if !strings.Contains(page.Text, want) {
		t.Errorf("в тексте нет фразы %q:\n%s", want, page.Text)
	}
	if !doc.Complete {
		t.Errorf("вердикт «с потерями» на файле с текстовым слоем: %+v", doc)
	}
	if ratio := sharedTextRatio(want, page.Text); ratio < 0.8 {
		t.Errorf("доля эталонных символов %.2f < 0.8", ratio)
	}
}

func TestParsePdfScannedPageUsesOcr(t *testing.T) {
	path := writePdfFixture(t, t.TempDir(), "scan.pdf", pdfScannedFixture)
	const want = "Scanned alpha bravo charlie delta"

	doc, err := ParsePdf(path)
	if err != nil {
		t.Fatalf("ParsePdf: %v", err)
	}
	if len(doc.Pages) != 1 {
		t.Fatalf("страниц %d, хотели 1", len(doc.Pages))
	}
	page := doc.Pages[0]
	if page.Method != PdfMethodOcr {
		t.Errorf("метод страницы = %q, хотели OCR: скан обязан распознаваться, а не считаться текстовым слоем", page.Method)
	}
	if !doc.Complete {
		t.Errorf("скан с успешным OCR не должен быть «с потерями»: %+v", doc)
	}
	// Критерий задачи: OCR достал ≥80 % символов эталонной фразы.
	if ratio := sharedTextRatio(want, page.Text); ratio < 0.8 {
		t.Errorf("OCR вернул %.2f долю эталонных символов (<0.8):\n%s", ratio, page.Text)
	}
	if doc.OcrPages != 1 {
		t.Errorf("OcrPages = %d, хотели 1", doc.OcrPages)
	}
	var noted bool
	for _, n := range doc.Notes {
		if strings.Contains(n, "распознано OCR") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("в Notes нет пометки об OCR: %v", doc.Notes)
	}
}

func TestParsePdfNotAPdf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "мусор.pdf")
	if err := os.WriteFile(path, []byte("PK\x03\x04 это не pdf, а ZIP-огрызок"), 0o600); err != nil {
		t.Fatalf("запись: %v", err)
	}
	if doc, err := ParsePdf(path); err == nil {
		t.Fatalf("битый pdf принят без ошибки: %+v", doc)
	}
}

func TestSplitPdfPages(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		could []string
	}{
		{"одна страница с финальным \f", "раз\n\f", []string{"раз"}},
		{"две страницы", "раз\n\fдва\n\f", []string{"раз\n", "два"}},
		{"пустая страница среди текста", "раз\n\f\fдва\n\f", []string{"раз", "", "два"}},
		{"пустая одна страница", "\f", []string{""}},
		{"пустой вывод", "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitPdfPages(tc.raw)
			if len(got) != len(tc.could) {
				t.Fatalf("частей %d (%q), хотели %d", len(got), got, len(tc.could))
			}
			for i := range got {
				if strings.TrimSpace(got[i]) != strings.TrimSpace(tc.could[i]) {
					t.Errorf("часть %d = %q, хотели %q", i, got[i], tc.could[i])
				}
			}
		})
	}
}

func TestNonSpaceRunes(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"   \t\n ", 0},
		{"abc", 3},
		{"a b\u00a0c\u2009", 3},
		{"тула, ул. пионеров", 16},
	}
	for _, tc := range tests {
		if got := nonSpaceRunes(tc.in); got != tc.want {
			t.Errorf("nonSpaceRunes(%q) = %d, хотели %d", tc.in, got, tc.want)
		}
	}
}
