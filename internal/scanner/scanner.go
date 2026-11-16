// Package scanner выполняет обход каталога с документами и определяет тип каждого файла.
//
// Задача P1.1 эпика P1 («Парсинг документов», poc.md): найти все файлы в переданном
// каталоге и классифицировать их по типу (.docx/.xlsx/.pdf и прочие). Путь к каталогу
// всегда приходит извне (CLI-флаг --input-dir) — пути в коде не фиксируются.
package scanner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind — классификация файла по его роли в конвейере P1.
type Kind string

const (
	KindDocx         Kind = "docx"
	KindXlsx         Kind = "xlsx"
	KindPdf          Kind = "pdf"
	KindOfficeLegacy Kind = "office_legacy"
	KindArchive      Kind = "archive"
	KindService      Kind = "service"
	KindOther        Kind = "other"
)

// Supported — типы, содержимое которых можно разобрать в задачах P1.2—P1.5.
func (k Kind) Supported() bool {
	return k == KindDocx || k == KindXlsx || k == KindPdf
}

// Как именно определён тип файла — важно для доверия к классификации.
const (
	DetectorBoth      = "extension+signature"
	DetectorSignature = "signature"
	DetectorExtension = "extension"
	DetectorName      = "name"
	DetectorNone      = "none"
)

// Entry — классифицированный файл.
type Entry struct {
	// RelPath — путь от корня сканирования с прямыми слэшами.
	RelPath string `json:"rel_path"`
	// Path — абсолютный путь, пригодный для открытия файла следующим шагом конвейера.
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`

	Kind       Kind   `json:"kind"`
	DetectedBy string `json:"detected_by"`
	Supported  bool   `json:"supported"`
	// Signature — что именно нашлось в содержимом (например «word/document.xml»).
	Signature string `json:"signature,omitempty"`
	// Mismatch — расширение противоречит содержимому: классифицируем по содержимому.
	Mismatch bool   `json:"mismatch"`
	Note     string `json:"note,omitempty"`
	// Problem — файл найден, но его состояние требует внимания человека (ошибка чтения, symlink).
	Problem string `json:"problem,omitempty"`
}

// Result — итог обхода каталога.
type Result struct {
	Root        string       `json:"root"`
	Entries     []Entry      `json:"entries"`
	Counts      map[Kind]int `json:"counts"`
	Total       int          `json:"total"`
	Supported   int          `json:"supported"`
	Mismatches  int          `json:"mismatches"`
	Problems    []string     `json:"problems,omitempty"`
	Notes       []string     `json:"notes,omitempty"`
	SkippedDirs []string     `json:"skipped_dirs,omitempty"`
}

// HasProblems — есть ли замечания, из-за которых результат нельзя считать чистым.
func (r *Result) HasProblems() bool {
	return len(r.Problems) > 0
}

// Scan рекурсивно обходит каталог inputDir и классифицирует все найденные файлы.
// Ошибки неверного пути возвращаются как понятные человеку формулировки.
func Scan(inputDir string) (*Result, error) {
	if strings.TrimSpace(inputDir) == "" {
		return nil, errors.New("не задан каталог с документами: передайте путь флагом --input-dir")
	}
	root, err := filepath.Abs(inputDir) // Abs заодно очищает путь: «docs//.» и «docs/» дадут один результат
	if err != nil {
		return nil, fmt.Errorf("не удалось развёрнуть путь %q: %w", inputDir, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("каталог не найден: %s — проверьте путь, переданный через --input-dir", root)
		}
		return nil, fmt.Errorf("нет доступа к каталогу %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s — файл, а не каталог: в --input-dir нужно передать папку с документами", root)
	}

	res := &Result{Root: root, Counts: map[Kind]int{}}
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		rel := relPath(root, path)
		if err != nil {
			// Повреждённый каталог или пропавший файл не должен обрывать весь обход.
			res.Problems = append(res.Problems, fmt.Sprintf("%s: %v", rel, err))
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != root && isSkippedDir(d.Name()) {
				res.SkippedDirs = append(res.SkippedDirs, rel)
				return fs.SkipDir
			}
			return nil
		}
		res.Entries = append(res.Entries, classifyFile(root, path, d))
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("обход каталога %s: %w", root, walkErr)
	}

	sort.Slice(res.Entries, func(i, j int) bool { return res.Entries[i].RelPath < res.Entries[j].RelPath })
	summarize(res)
	if res.Total == 0 {
		res.Notes = append(res.Notes, "в каталоге не найдено ни одного файла — проверьте правильность пути")
	}
	return res, nil
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func classifyFile(root, path string, d fs.DirEntry) Entry {
	e := Entry{RelPath: relPath(root, path), Path: path, Name: filepath.Base(path)}

	info, err := d.Info()
	if err != nil {
		e.Kind = KindOther
		e.DetectedBy = DetectorNone
		e.Problem = fmt.Sprintf("не удалось получить сведения о файле: %v", err)
		return e
	}
	e.Size = info.Size()
	e.ModTime = info.ModTime()

	if d.Type()&fs.ModeSymlink != 0 {
		e.Kind = KindOther
		e.DetectedBy = DetectorName
		e.Problem = "символическая ссылка: обход по ссылкам не выполняется (риск зацикливания)"
		return e
	}
	if !d.Type().IsRegular() {
		e.Kind = KindOther
		e.DetectedBy = DetectorName
		e.Problem = "не обычный файл (канал, сокет или устройство) — пропускается"
		return e
	}

	sig, err := readSignature(path)
	if err != nil {
		e.Kind = kindByExtension(e.Name)
		e.DetectedBy = DetectorExtension
		e.Supported = e.Kind.Supported()
		e.Problem = fmt.Sprintf("не удалось прочитать содержимое: %v", err)
		e.Note = "тип определён только по расширению"
		return e
	}

	e.Kind, e.DetectedBy, e.Mismatch, e.Note = classify(e.Name, sig)
	e.Signature = sig.Detail
	e.Supported = e.Kind.Supported()
	if e.Mismatch && e.Note == "" {
		e.Note = "классифицировано по содержимому"
	}
	return e
}

func summarize(res *Result) {
	res.Total = len(res.Entries)
	res.Supported = 0
	res.Mismatches = 0
	for _, e := range res.Entries {
		res.Counts[e.Kind]++
		if e.Supported {
			res.Supported++
		}
		if e.Mismatch {
			res.Mismatches++
		}
		if e.Problem != "" {
			res.Problems = append(res.Problems, fmt.Sprintf("%s: %s", e.RelPath, e.Problem))
		}
	}
}

// kindByExtension — тип по расширению, используемый, когда содержимое не читается.
func kindByExtension(name string) Kind {
	if k, ok := kindByExt[strings.ToLower(filepath.Ext(name))]; ok {
		return k
	}
	return KindOther
}

// isSkippedDir — каталоги, которые не относятся к документу и раздувают обход.
func isSkippedDir(name string) bool {
	switch name {
	case ".git", "$RECYCLE.BIN", "System Volume Information":
		return true
	default:
		return false
	}
}
