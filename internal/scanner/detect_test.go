package scanner

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zipMember — элемент архива; порядок важен для воспроизводимости тестов.
type zipMember struct {
	Name string
	Body string
}

func writeZipFile(t *testing.T, path string, members []zipMember) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("не удалось создать %s: %v", path, err)
	}
	w := zip.NewWriter(f)
	for _, m := range members {
		fw, err := w.Create(m.Name)
		if err != nil {
			f.Close()
			t.Fatalf("zip.Create(%s): %v", m.Name, err)
		}
		if _, err := fw.Write([]byte(m.Body)); err != nil {
			f.Close()
			t.Fatalf("запись %s: %v", m.Name, err)
		}
	}
	if err := w.Close(); err != nil {
		f.Close()
		t.Fatalf("закрытие zip-писателя: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("закрытие %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()

	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("не удалось записать %s: %v", path, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("не удалось создать каталог %s: %v", path, err)
	}
}

func docxMembers(name string) []zipMember {
	return []zipMember{
		{Name: "[Content_Types].xml", Body: `<?xml version="1.0"?><Types/>`},
		{Name: "word/document.xml", Body: `<w:document>` + name + `</w:document>`},
	}
}

func xlsxMembers() []zipMember {
	return []zipMember{
		{Name: "[Content_Types].xml", Body: `<?xml version="1.0"?><Types/>`},
		{Name: "xl/workbook.xml", Body: `<workbook/>`},
	}
}

const (
	pdfBody   = "%PDF-1.7\n1 0 obj<</Type/Catalog>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n"
	oleHeader = "\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1 legacy office"
)

func TestReadSignature(t *testing.T) {
	root := t.TempDir()

	docx := filepath.Join(root, "doc.docx")
	writeZipFile(t, docx, docxMembers("тз"))

	xlsx := filepath.Join(root, "spec.xlsx")
	writeZipFile(t, xlsx, xlsxMembers())

	pdf := filepath.Join(root, "scan.pdf")
	writeFile(t, pdf, []byte(pdfBody))

	empty := filepath.Join(root, "empty.docx")
	writeFile(t, empty, nil)

	ole := filepath.Join(root, "old.doc")
	writeFile(t, ole, []byte(oleHeader))

	rtf := filepath.Join(root, "letter.rtf")
	writeFile(t, rtf, []byte(`{\rtf1\ansi Прив}`))

	plain := filepath.Join(root, "notes.txt")
	writeFile(t, plain, []byte("обычный текст без сигнатуры формата"))

	archive := filepath.Join(root, "bundle.zip")
	writeZipFile(t, archive, []zipMember{{Name: "readme.txt", Body: "содержимое архива"}})

	corrupt := filepath.Join(root, "broken.docx")
	writeFile(t, corrupt, append([]byte{'P', 'K', 0x03, 0x04}, []byte("хвост не является zip-записью")...))

	odf := filepath.Join(root, "form.odt")
	writeZipFile(t, odf, []zipMember{{Name: "mimetype", Body: "application/vnd.oasis.opendocument.text"}})

	missing := filepath.Join(root, "нет-такого-файла.docx")

	tests := []struct {
		name       string
		path       string
		wantKind   Kind
		wantEmpty  bool
		wantDetail string // подстрока
		wantErr    bool
	}{
		{name: "docx по структуре OOXML", path: docx, wantKind: KindDocx, wantDetail: "word/document.xml"},
		{name: "xlsx по структуре OOXML", path: xlsx, wantKind: KindXlsx, wantDetail: "xl/workbook.xml"},
		{name: "pdf по заголовку", path: pdf, wantKind: KindPdf, wantDetail: "PDF"},
		{name: "пустой файл", path: empty, wantKind: KindOther, wantEmpty: true, wantDetail: "пустой файл"},
		{name: "старый бинарный office", path: ole, wantKind: KindOfficeLegacy, wantDetail: "OLE"},
		{name: "rtf вне P1", path: rtf, wantKind: KindOther, wantDetail: "RTF"},
		{name: "обычный текст", path: plain, wantKind: KindOther, wantDetail: "не распознана"},
		{name: "zip без OOXML", path: archive, wantKind: KindArchive, wantDetail: "ZIP без структуры"},
		{name: "повреждённый zip остаётся архивом", path: corrupt, wantKind: KindArchive, wantDetail: "повреждённый ZIP"},
		{name: "odf распознаётся как архив", path: odf, wantKind: KindArchive, wantDetail: "OpenDocument"},
		{name: "файл не найден", path: missing, wantKind: KindOther, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readSignature(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ожидали ошибку для %s, получили %+v", tc.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("readSignature(%s): непредвиденная ошибка %v", tc.path, err)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q, хотели %q", got.Kind, tc.wantKind)
			}
			if got.Empty != tc.wantEmpty {
				t.Errorf("Empty = %v, хотели %v", got.Empty, tc.wantEmpty)
			}
			if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("Detail = %q, хотели подстроку %q", got.Detail, tc.wantDetail)
			}
		})
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name           string
		fileName       string
		sig            signature
		wantKind       Kind
		wantDetector   string
		wantMismatch   bool
		wantNoteSubstr string
	}{
		{
			name:     "расширение и содержимое согласованы",
			fileName: "ТЗ (ред. от 19.10.2026).docx",
			sig:      signature{Kind: KindDocx, Detail: "word/document.xml"},
			wantKind: KindDocx, wantDetector: DetectorBoth, wantMismatch: false, wantNoteSubstr: "",
		},
		{
			name:     "pdf подтверждён заголовком",
			fileName: "Заявка/1скан.pdf",
			sig:      signature{Kind: KindPdf, Detail: "PDF"},
			wantKind: KindPdf, wantDetector: DetectorBoth,
		},
		{
			name:     "xlsx под названием docx берём из содержимого",
			fileName: "mislabeled.docx",
			sig:      signature{Kind: KindXlsx, Detail: "xl/workbook.xml"},
			wantKind: KindXlsx, wantDetector: DetectorSignature, wantMismatch: true,
			wantNoteSubstr: "расширение .docx",
		},
		{
			name:     "docx внутри pdf-расширения",
			fileName: "226717 Извещение ЭФ 223-ФЗ.pdf",
			sig:      signature{Kind: KindDocx, Detail: "word/document.xml"},
			wantKind: KindDocx, wantDetector: DetectorSignature, wantMismatch: true,
			wantNoteSubstr: "содержимое — docx",
		},
		{
			name:     "расширение верно, сигнатура не читается — доверяем расширению",
			fileName: "Заявка/опись.PDF",
			sig:      signature{Kind: KindOther, Detail: "сигнатура не распознана"},
			wantKind: KindPdf, wantDetector: DetectorExtension,
			wantNoteSubstr: "только по расширению",
		},
		{
			name:     "пустой docx бракуем",
			fileName: "1Опись.docx",
			sig:      signature{Kind: KindOther, Detail: "пустой файл", Empty: true},
			wantKind: KindOther, wantDetector: DetectorExtension, wantMismatch: true,
			wantNoteSubstr: "пустой файл",
		},
		{
			name:     "старый .doc помечаем конвертером",
			fileName: "устаревшая форма.doc",
			sig:      signature{Kind: KindOfficeLegacy, Detail: "OLE"},
			wantKind: KindOfficeLegacy, wantDetector: DetectorSignature,
			wantNoteSubstr: "нужен конвертер",
		},
		{
			name:     ".docx с бинарным содержимым — противоречие",
			fileName: "form.docx",
			sig:      signature{Kind: KindOfficeLegacy, Detail: "OLE"},
			wantKind: KindOfficeLegacy, wantDetector: DetectorSignature, wantMismatch: true,
			wantNoteSubstr: "парсер OOXML/PDF",
		},
		{
			name:     "zip-дубликат папки",
			fileName: "Пакет документов.zip",
			sig:      signature{Kind: KindArchive, Detail: "ZIP без структуры OOXML"},
			wantKind: KindArchive, wantDetector: DetectorSignature,
			wantNoteSubstr: "архив вне перечня P1",
		},
		{
			name:     "pptx вне перечня P1",
			fileName: "презентация.pptx",
			sig:      signature{Kind: KindArchive, Detail: "ZIP без структуры OOXML"},
			wantKind: KindArchive, wantDetector: DetectorSignature, wantMismatch: false,
			wantNoteSubstr: "архив вне перечня P1",
		},
		{
			name:     "lock-файл libreoffice",
			fileName: ".~lock.226717 Извещение ЭФ 223-ФЗ.docx#",
			sig:      signature{Kind: KindOther, Detail: "сигнатура не распознана"},
			wantKind: KindService, wantDetector: DetectorName,
			wantNoteSubstr: "служебный файл",
		},
		{
			name:     "lock-файл ms office",
			fileName: "~$226717 ЗД ЭФ (К, КП, ЗП).docx",
			sig:      signature{Kind: KindDocx, Detail: "word/document.xml"},
			wantKind: KindService, wantDetector: DetectorName,
			wantNoteSubstr: "служебный файл",
		},
		{
			name:     "файл ОС",
			fileName: ".DS_Store",
			sig:      signature{Kind: KindOther, Detail: "сигнатура не распознана"},
			wantKind: KindService, wantDetector: DetectorName,
			wantNoteSubstr: "служебный файл",
		},
		{
			name:     "неизвестное расширение",
			fileName: "фото скана.jpg",
			sig:      signature{Kind: KindOther, Detail: "сигнатура не распознана"},
			wantKind: KindOther, wantDetector: DetectorNone, wantNoteSubstr: "не входит в P1",
		},
		{
			name:     "файл без расширения",
			fileName: "README",
			sig:      signature{Kind: KindOther, Detail: "сигнатура не распознана"},
			wantKind: KindOther, wantDetector: DetectorNone, wantNoteSubstr: "без расширения",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, detector, mismatch, note := classify(tc.fileName, tc.sig)
			if kind != tc.wantKind {
				t.Errorf("kind = %q, хотели %q", kind, tc.wantKind)
			}
			if detector != tc.wantDetector {
				t.Errorf("detector = %q, хотели %q", detector, tc.wantDetector)
			}
			if mismatch != tc.wantMismatch {
				t.Errorf("mismatch = %v, хотели %v", mismatch, tc.wantMismatch)
			}
			if tc.wantNoteSubstr == "" {
				if note != "" {
					t.Errorf("note = %q, хотели пустое", note)
				}
				return
			}
			if !strings.Contains(note, tc.wantNoteSubstr) {
				t.Errorf("note = %q, хотели подстроку %q", note, tc.wantNoteSubstr)
			}
		})
	}
}

func TestKindSupported(t *testing.T) {
	tests := []struct {
		kind Kind
		want bool
	}{
		{KindDocx, true}, {KindXlsx, true}, {KindPdf, true},
		{KindOfficeLegacy, false}, {KindArchive, false}, {KindService, false}, {KindOther, false},
	}
	for _, tc := range tests {
		t.Run(string(tc.kind), func(t *testing.T) {
			if got := tc.kind.Supported(); got != tc.want {
				t.Errorf("Supported() = %v, хотели %v", got, tc.want)
			}
		})
	}
}

func TestIsServiceFile(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{".~lock.226717 ЗД ЭФ.docx#", true},
		{"~$Заявка.docx", true},
		{"Thumbs.db", true},
		{"desktop.ini", true},
		{"document.tmp", true},
		{"файл.docx.part", true},
		{"1Опись.docx", false},
		{"spec.xlsx", false},
		{"скан.pdf", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isServiceFile(tc.name); got != tc.want {
				t.Errorf("isServiceFile(%q) = %v, хотели %v", tc.name, got, tc.want)
			}
		})
	}
}
