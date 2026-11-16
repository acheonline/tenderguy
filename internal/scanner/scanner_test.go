package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanClassifiesWholeDirectoryTree(t *testing.T) {
	root := t.TempDir()

	// Структура повторяет реальные пакеты из docs/: вложенные каталоги, кириллица в именах,
	// lock-файл редактора, zip-дубликат папки и файл с неверным расширением.
	mustMkdir(t, filepath.Join(root, "Приложение 1_ТЗ"))
	mustMkdir(t, filepath.Join(root, "Заявка"))

	writeZipFile(t, filepath.Join(root, "226717 Извещение ЭФ 223-ФЗ.docx"), docxMembers("извещение"))
	writeZipFile(t, filepath.Join(root, "Приложение 1_ТЗ", "ТЗ (ред. от 19.10.2026).docx"), docxMembers("тз"))
	writeZipFile(t, filepath.Join(root, "Приложение 1_ТЗ", "226717_671.26.00374_ТЕХ.xlsx"), xlsxMembers())
	writeFile(t, filepath.Join(root, "Приложение 1_ТЗ", "как заполнить.xlsx.txt"), []byte("памятка"))
	writeZipFile(t, filepath.Join(root, "Заявка", "1Опись.docx"), docxMembers("опись"))
	writeFile(t, filepath.Join(root, "Заявка", "22выписка.pdf"), []byte(pdfBody))
	writeZipFile(t, filepath.Join(root, "Заявка", "16Спецификация.docx"), xlsxMembers()) // расширение врёт содержимому
	writeFile(t, filepath.Join(root, "Заявка", "9пустой.pdf"), nil)
	writeZipFile(t, filepath.Join(root, "Пакет документов.zip"), []zipMember{{Name: "readme.txt", Body: "дубликат папки"}})
	writeFile(t, filepath.Join(root, ".~lock.226717 Извещение ЭФ 223-ФЗ.docx#"), []byte("lock"))

	mustMkdir(t, filepath.Join(root, ".git", "objects"))
	writeFile(t, filepath.Join(root, ".git", "objects", "pack.idx"), []byte("не документ"))

	link := filepath.Join(root, "Заявка", "ссылка.pdf")
	if err := os.Symlink(filepath.Join(root, "Заявка", "22выписка.pdf"), link); err != nil {
		t.Skipf("символические ссылки не поддерживаются средой: %v", err)
	}

	res, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if res.Root != filepath.Clean(root) {
		t.Errorf("Root = %q, хотели %q", res.Root, filepath.Clean(root))
	}

	byPath := map[string]Entry{}
	for _, e := range res.Entries {
		byPath[e.RelPath] = e
	}

	tests := []struct {
		relPath     string
		wantKind    Kind
		wantSupport bool
		wantBy      string
		wantMatch   bool
	}{
		{relPath: "226717 Извещение ЭФ 223-ФЗ.docx", wantKind: KindDocx, wantSupport: true, wantBy: DetectorBoth},
		{relPath: "Приложение 1_ТЗ/ТЗ (ред. от 19.10.2026).docx", wantKind: KindDocx, wantSupport: true, wantBy: DetectorBoth},
		{relPath: "Приложение 1_ТЗ/226717_671.26.00374_ТЕХ.xlsx", wantKind: KindXlsx, wantSupport: true, wantBy: DetectorBoth},
		{relPath: "Приложение 1_ТЗ/как заполнить.xlsx.txt", wantKind: KindOther, wantSupport: false, wantBy: DetectorNone},
		{relPath: "Заявка/1Опись.docx", wantKind: KindDocx, wantSupport: true, wantBy: DetectorBoth},
		{relPath: "Заявка/22выписка.pdf", wantKind: KindPdf, wantSupport: true, wantBy: DetectorBoth},
		{relPath: "Заявка/16Спецификация.docx", wantKind: KindXlsx, wantSupport: true, wantBy: DetectorSignature, wantMatch: true},
		{relPath: "Заявка/9пустой.pdf", wantKind: KindOther, wantSupport: false, wantBy: DetectorExtension, wantMatch: true},
		{relPath: "Пакет документов.zip", wantKind: KindArchive, wantSupport: false, wantBy: DetectorSignature},
		{relPath: ".~lock.226717 Извещение ЭФ 223-ФЗ.docx#", wantKind: KindService, wantSupport: false, wantBy: DetectorName},
		{relPath: "Заявка/ссылка.pdf", wantKind: KindOther, wantSupport: false, wantBy: DetectorName},
	}

	for _, tc := range tests {
		t.Run(tc.relPath, func(t *testing.T) {
			got, ok := byPath[tc.relPath]
			if !ok {
				t.Fatalf("файл %s не найден обходом; найдены: %s", tc.relPath, foundPaths(res))
			}
			if got.Kind != tc.wantKind {
				t.Errorf("Kind = %q, хотели %q", got.Kind, tc.wantKind)
			}
			if got.Supported != tc.wantSupport {
				t.Errorf("Supported = %v, хотели %v", got.Supported, tc.wantSupport)
			}
			if got.DetectedBy != tc.wantBy {
				t.Errorf("DetectedBy = %q, хотели %q", got.DetectedBy, tc.wantBy)
			}
			if got.Mismatch != tc.wantMatch {
				t.Errorf("Mismatch = %v, хотели %v", got.Mismatch, tc.wantMatch)
			}
			if got.Path == "" || got.Name == "" {
				t.Errorf("Path/Name не заполнены: %+v", got)
			}
		})
	}

	// Файлы внутри .git не должны попадать в обход.
	for p := range byPath {
		if strings.HasPrefix(p, ".git/") {
			t.Errorf("каталог %s не был пропущен", p)
		}
	}
	if !strings.Contains(strings.Join(res.SkippedDirs, " "), ".git") {
		t.Errorf("SkippedDirs = %v, хотели упоминание .git", res.SkippedDirs)
	}

	if res.Total != len(byPath) {
		t.Errorf("Total = %d, записей = %d", res.Total, len(byPath))
	}
	if res.Total != 11 {
		t.Errorf("Total = %d, хотели 11 найденных файлов", res.Total)
	}
	if res.Supported != 6 {
		t.Errorf("Supported = %d, хотели 6", res.Supported)
	}
	if res.Mismatches != 2 {
		t.Errorf("Mismatches = %d, хотели 2", res.Mismatches)
	}
	if res.Counts[KindDocx] != 3 || res.Counts[KindXlsx] != 2 ||
		res.Counts[KindPdf] != 1 || res.Counts[KindService] != 1 {
		t.Errorf("Counts = %v, ожидали docx=3, xlsx=2, pdf=1, service=1", res.Counts)
	}
	if len(res.Problems) != 1 || !strings.Contains(res.Problems[0], "ссылка.pdf") {
		t.Errorf("Problems = %v, хотели одну проблему по symlink", res.Problems)
	}
	if !isSortedEntries(res.Entries) {
		t.Errorf("записи не отсортированы по RelPath: %s", foundPaths(res))
	}
}

func TestScanEmptyDirectoryGivesHint(t *testing.T) {
	res, err := Scan(t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.Total != 0 {
		t.Fatalf("Total = %d, хотели 0", res.Total)
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "ни одного файла") {
		t.Errorf("Notes = %v, хотели подсказку о проверке пути", res.Notes)
	}
}

func TestScanPathErrors(t *testing.T) {
	dir := t.TempDir()
	aFile := filepath.Join(dir, "документ.pdf")
	writeFile(t, aFile, []byte(pdfBody))

	tests := []struct {
		name    string
		input   string
		wantSub string
	}{
		{name: "каталог не существует", input: filepath.Join(dir, "нет-такого-каталога"), wantSub: "каталог не найден"},
		{name: "передан файл вместо каталога", input: aFile, wantSub: "файл, а не каталог"},
		{name: "пустой путь", input: "", wantSub: "не задан каталог"},
		{name: "только пробелы", input: "   ", wantSub: "не задан каталог"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Scan(tc.input)
			if err == nil {
				t.Fatalf("ожидали ошибку, получили %+v", res)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("ошибка = %q, хотели подстроку %q", err.Error(), tc.wantSub)
			}
			if res != nil {
				t.Errorf("при ошибке результат должен быть пуст, получили %+v", res)
			}
		})
	}
}

func TestScanRelativePathAndUnreadableFile(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "Заявка"))
	writeZipFile(t, filepath.Join(root, "Заявка", "1Опись.docx"), docxMembers("опись"))

	bad := filepath.Join(root, "locked.docx")
	writeZipFile(t, bad, docxMembers("нет доступа"))
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(bad, 0o600); err != nil {
			t.Errorf("возврат прав: %v", err)
		}
	})

	// Относительный путь — так --input-dir приходит из CI или от пользователя.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Errorf("возврат рабочего каталога: %v", err)
		}
	}()

	res, err := Scan("Заявка")
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(res.Entries) != 1 || res.Entries[0].RelPath != "1Опись.docx" {
		t.Fatalf("Entries = %+v, хотели один документ", res.Entries)
	}
	if !filepath.IsAbs(res.Entries[0].Path) {
		t.Errorf("Path = %q, хотели абсолютный путь", res.Entries[0].Path)
	}
	if res.Entries[0].Size <= 0 {
		t.Errorf("Size = %d, хотели положительный размер", res.Entries[0].Size)
	}

	// Читаемость без прав проверяется только не от root: root читает любой файл.
	if os.Geteuid() == 0 {
		t.Log("эффективный uid 0: права доступа не проверяются")
		return
	}
	full, err := Scan(root)
	if err != nil {
		t.Fatalf("Scan(root): %v", err)
	}
	var found *Entry
	for i, e := range full.Entries {
		if e.RelPath == "locked.docx" {
			found = &full.Entries[i]
		}
	}
	if found == nil {
		t.Fatalf("locked.docx не найден среди %+v", full.Entries)
	}
	if found.Problem == "" {
		t.Errorf("Problem пуст, хотели ошибку чтения для файла без прав")
	}
	if found.Kind != KindDocx {
		t.Errorf("Kind = %q, хотели docx по расширению", found.Kind)
	}
}

func isSortedEntries(entries []Entry) bool {
	for i := 1; i < len(entries); i++ {
		if entries[i-1].RelPath > entries[i].RelPath {
			return false
		}
	}
	return true
}

func foundPaths(res *Result) string {
	paths := make([]string, 0, len(res.Entries))
	for _, e := range res.Entries {
		paths = append(paths, e.RelPath)
	}
	return strings.Join(paths, ", ")
}
