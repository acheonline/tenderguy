package scanner

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// sizeSignatureHead — сколько байт начала файла достаточно для распознавания формата.
// Заголовок PDF по спецификации обязан стоять в начале файла, поэтому 1 KiB с запасом.
const sizeSignatureHead = 1024

var (
	magicZip = []byte{'P', 'K', 0x03, 0x04}
	magicOLE = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
	magicPDF = []byte("%PDF-")
	magicRTF = []byte("{\\rtf")
)

// kindByExt — ожидаемый тип по расширению. .doc/.xls/.ppt в PoC не разбираются:
// это старый бинарный формат, отдельная ветка обработки.
var kindByExt = map[string]Kind{
	".docx": KindDocx,
	".xlsx": KindXlsx,
	".pdf":  KindPdf,
	".doc":  KindOfficeLegacy,
	".xls":  KindOfficeLegacy,
	".ppt":  KindOfficeLegacy,
	".pptx": KindArchive,
	".zip":  KindArchive,
}

// signature — что показал разбор начала файла.
type signature struct {
	Kind   Kind
	Detail string
	// Empty — файл нулевой длины: расширению доверять нельзя.
	Empty bool
}

// readSignature определяет формат по содержимому файла, а не по имени.
func readSignature(path string) (sig signature, err error) {
	f, err := os.Open(path) //nolint:gosec // путь получен из обхода каталога, переданного пользователем
	if err != nil {
		return signature{}, fmt.Errorf("открытие: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("закрытие: %w", closeErr)
		}
	}()

	head := make([]byte, sizeSignatureHead)
	n, readErr := io.ReadFull(f, head)
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return signature{}, fmt.Errorf("чтение заголовка: %w", readErr)
	}
	head = head[:n]
	if n == 0 {
		return signature{Kind: KindOther, Detail: "пустой файл", Empty: true}, nil
	}

	if idx := bytes.Index(head, magicPDF); idx >= 0 && idx <= 16 {
		return signature{Kind: KindPdf, Detail: "PDF"}, nil
	}
	if bytes.HasPrefix(head, magicZip) {
		return readZipSignature(f, path)
	}
	if bytes.HasPrefix(head, magicOLE) {
		return signature{Kind: KindOfficeLegacy, Detail: "OLE (бинарный MS Office .doc/.xls/.ppt)"}, nil
	}
	if bytes.HasPrefix(head, magicRTF) {
		return signature{Kind: KindOther, Detail: "RTF"}, nil
	}
	return signature{Kind: KindOther, Detail: "сигнатура не распознана"}, nil
}

// readZipSignature различает контейнеры OOXML: .docx, .xlsx и прочие ZIP-архивы.
// Файл уже открыт и его размер известен из заголовка ZIP, поэтому читается центральный каталог.
func readZipSignature(f *os.File, path string) (signature, error) {
	info, err := f.Stat()
	if err != nil {
		return signature{}, fmt.Errorf("размер %s: %w", filepath.Base(path), err)
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		// Повреждённый архив — не ошибка обхода: тип известен (ZIP), разбор будет невозможен.
		return signature{Kind: KindArchive, Detail: "повреждённый ZIP"}, nil
	}
	for _, fh := range zr.File {
		name := filepath.ToSlash(fh.Name)
		switch {
		case strings.HasPrefix(name, "word/"):
			return signature{Kind: KindDocx, Detail: name}, nil
		case strings.HasPrefix(name, "xl/"):
			return signature{Kind: KindXlsx, Detail: name}, nil
		case name == "mimetype":
			return signature{Kind: KindArchive, Detail: "OpenDocument (ODF)"}, nil
		}
	}
	return signature{Kind: KindArchive, Detail: "ZIP без структуры OOXML"}, nil
}

// classify сводит расширение и содержимое в единый вердикт.
// Возвращает тип, способ определения, признак противоречия и примечание для отчёта.
func classify(name string, sig signature) (kind Kind, detector string, mismatch bool, note string) {
	ext := strings.ToLower(filepath.Ext(name))
	extKind, extKnown := kindByExt[ext]

	if isServiceFile(name) {
		return KindService, DetectorName, false, "служебный файл редактора или ОС — не документ"
	}

	switch sig.Kind {
	case KindDocx, KindXlsx, KindPdf:
		switch {
		case extKnown && extKind == sig.Kind:
			return sig.Kind, DetectorBoth, false, ""
		case extKnown:
			return sig.Kind, DetectorSignature, true,
				fmt.Sprintf("расширение %s, содержимое — %s; тип взят из содержимого", ext, sig.Kind)
		default:
			return sig.Kind, DetectorSignature, false,
				fmt.Sprintf("тип определён по содержимому, расширение %q нестандартное", ext)
		}

	case KindOfficeLegacy:
		if extKnown && extKind.Supported() {
			return sig.Kind, DetectorSignature, true, fmt.Sprintf(
				"расширение %s, содержимое — бинарный MS Office: парсер OOXML/PDF с этим файлом не сработает", ext)
		}
		return sig.Kind, DetectorSignature, false, "старый бинарный MS Office (.doc/.xls/.ppt) — вне P1, нужен конвертер"

	case KindArchive:
		if extKnown && extKind.Supported() {
			return sig.Kind, DetectorSignature, true, fmt.Sprintf(
				"расширение %s, содержимое — ZIP-архив: парсер OOXML/PDF с этим файлом не сработает", ext)
		}
		return sig.Kind, DetectorSignature, false, "архив вне перечня P1 (.docx/.xlsx/.pdf)"

	default:
		if sig.Empty && extKnown {
			return KindOther, DetectorExtension, true, "пустой файл (0 байт) — извлекать нечего"
		}
		if extKnown {
			return extKind, DetectorExtension, false, "сигнатура не распознана, тип определён только по расширению"
		}
		if ext == "" {
			return KindOther, DetectorNone, false, "файл без расширения и без распознаваемой сигнатуры"
		}
		return KindOther, DetectorNone, false, fmt.Sprintf("формат %s не входит в P1", ext)
	}
}

// isServiceFile отсекает служебные файлы, которые лежат рядом с документами:
// lock-файлы LibreOffice (".~lock.<имя>#") и MS Office ("~$<имя>"), файлы ОС и временные хвосты.
func isServiceFile(name string) bool {
	if strings.HasPrefix(name, ".~lock.") && strings.HasSuffix(name, "#") {
		return true
	}
	if strings.HasPrefix(name, "~$") {
		return true
	}
	lower := strings.ToLower(name)
	switch lower {
	case ".ds_store", "thumbs.db", "desktop.ini", "ehthumbs.db":
		return true
	}
	for _, suffix := range []string{".tmp", ".part", ".crdownload", ".swp"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}
