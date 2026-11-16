package parser

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/otiai10/gosseract/v2"
)

// Задача P1.4 — разбор PDF. Текстовый слой читает poppler (`pdftotext`),
// страницы без текстового слоя (сканы) распознаёт Tesseract через
// `github.com/otiai10/gosseract`: страница рендерится `pdftoppm` в PNG,
// картинка уходит в OCR. Языки распознавания — русский и английский:
// заявки 44-ФЗ/223-ФЗ русские, инкодованные вставки (названия firm, ГОСТ) — английские.

// Форматы содержимого, которые умеет разбирать конвейер P1.
const FormatPdf = "pdf"

// Способы, каким страница отдала текст.
const (
	PdfMethodText = "text" // текстовый слой PDF
	PdfMethodOcr  = "ocr"  // скан, распознан Tesseract
)

// minTextLayerRunes — страниц с меньшим числом непространственных символов
// считается сканом: у реальных сканов текстового слоя нет вовсе, максимум —
// водяной знак или артефакт конвертера.
const minTextLayerRunes = 12

// ocrDPI — плотность растеризации страницы под OCR. 150 dpi — нижняя граница
// читаемости machine-print по конвенции Tesseract; ниже — деградация русского
// текста, выше — медленнее без выигрыша на типовых сканах заявочных PDF.
const ocrDPI = "150"

// PdfPage — одна страница: метод извлечения, текст и его объём.
type PdfPage struct {
	// Index — номер страницы, начиная с 1: координата для трассировки (P1.6).
	Index int `json:"index"`
	// Method — PdfMethodText или PdfMethodOcr.
	Method string `json:"method"`
	Text   string `json:"text"`
	// Runes — непространственные символы в Text; по ним судят о пустоте страницы.
	Runes int `json:"runes"`
}

// PdfDocument — результат разбора одного PDF. Полнота здесь измеряется иначе,
// чем в .docx: эталона «сколько символов должно быть» у PDF нет, поэтому
// Complete означает, что ни одна страница не осталась пустой — текст с каждой
// страницы дошёл до модели хотя бы одним из двух способов.
type PdfDocument struct {
	Path   string    `json:"path"`
	Name   string    `json:"name"`
	Format string    `json:"format"`
	Pages  []PdfPage `json:"pages"`

	TextPages int `json:"text_pages"`
	OcrPages  int `json:"ocr_pages"`
	// Runes — суммарный объём извлечённого текста без пробелов.
	Runes int `json:"runes"`
	// EmptyPages — страницы, с которых не удалось извлечь ничего: это потери.
	EmptyPages int  `json:"empty_pages"`
	Complete   bool `json:"complete"`
	// Notes — замечания разбора: какие страницы ушли в OCR, где OCR не справился.
	Notes []string `json:"notes,omitempty"`
}

// Text возвращает текст всех страниц в порядковом виде, страницы разделены
// пустой строкой.
func (d *PdfDocument) Text() string {
	parts := make([]string, 0, len(d.Pages))
	for _, p := range d.Pages {
		parts = append(parts, p.Text)
	}
	return strings.Join(parts, "\n\n")
}

// ParsePdf извлекает текст из PDF: текстовый слой, а пустые страницы — OCR.
// Ошибкой считается только случай, когда разбирать нечего или разбор невозможен
// целиком (битый/защищённый файл, нет инструментов OCR); пустая страница —
// замечание модели, а не ошибка вызова.
func ParsePdf(path string) (*PdfDocument, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("открытие pdf-файла: %w", err)
	}
	raw, err := pdfToText(path)
	if err != nil {
		return nil, err
	}
	texts := splitPdfPages(raw)
	if len(texts) == 0 {
		return nil, fmt.Errorf("pdftotext не вернул ни одной страницы: %s", filepath.Base(path))
	}

	doc := &PdfDocument{Path: path, Name: filepath.Base(path), Format: FormatPdf}
	for i, text := range texts {
		page := PdfPage{Index: i + 1, Method: PdfMethodText, Text: strings.TrimSpace(text)}
		page.Runes = nonSpaceRunes(page.Text)
		if page.Runes < minTextLayerRunes {
			ocrText, ocrErr := ocrPdfPage(path, page.Index)
			if ocrErr != nil {
				if errors.Is(ocrErr, exec.ErrNotFound) {
					// Скан, а инструментов нет — environment problem, а не свойство файла.
					return nil, fmt.Errorf("страница %d — скан без текстового слоя, а OCR-инструменты не установлены: %w",
						page.Index, ocrErr)
				}
				doc.Notes = append(doc.Notes, fmt.Sprintf("страница %d: OCR не удалось — %v", page.Index, ocrErr))
				page.Method = PdfMethodOcr
			} else {
				page.Text = strings.TrimSpace(ocrText)
				page.Runes = nonSpaceRunes(page.Text)
				page.Method = PdfMethodOcr
				doc.Notes = append(doc.Notes,
					fmt.Sprintf("страница %d: текстового слоя нет, распознано OCR (%s)", page.Index, strings.Join(ocrLanguages, "+")))
			}
		}
		if page.Runes == 0 {
			doc.EmptyPages++
		}
		if page.Method == PdfMethodOcr {
			doc.OcrPages++
		} else {
			doc.TextPages++
		}
		doc.Runes += page.Runes
		doc.Pages = append(doc.Pages, page)
	}
	doc.Complete = doc.EmptyPages == 0
	return doc, nil
}

// ocrLanguages — языковые пакеты Tesseract для заявочных документов.
var ocrLanguages = []string{"rus", "eng"}

// pdfToText запускает pdftotext на весь файл; страницы разделены \f.
func pdfToText(path string) (string, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("pdftotext", path, "-") //nolint:gosec // путь получен из обхода или флага --file
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("pdftotext не найден — нужен poppler-utils: %w", err)
		}
		msg := firstLine(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("pdftotext: %s", msg)
	}
	return stdout.String(), nil
}

// splitPdfPages режет вывод pdftotext по перевороту страницы. pdftotext
// ставит \f в конце каждой страницы, в том числе последней: один финальный
// \f — terminator, а не новая страница; пустая страница при этом остаётся
// пустой частью, иначе «скан без текстового слоя» исчез бы из отчёта.
func splitPdfPages(raw string) []string {
	if raw == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(raw, "\f"), "\f")
}

// ocrPdfPage растеризует страницу pdftoppm и отдаёт её Tesseract.
func ocrPdfPage(path string, page int) (string, error) {
	dir, err := os.MkdirTemp("", "tenderguy-ocr-")
	if err != nil {
		return "", fmt.Errorf("временный каталог рендера: %w", err)
	}
	defer func() {
		// Убрать мусор не удастся лишь на ошибке удаления — она не отменяет разбор.
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			fmt.Fprintf(os.Stderr, "tenderguy: не убран временный каталог %s: %v\n", dir, rmErr)
		}
	}()

	base := filepath.Join(dir, "page")
	var renderErr bytes.Buffer
	render := exec.Command("pdftoppm", "-png", "-r", ocrDPI,
		"-f", strconv.Itoa(page), "-l", strconv.Itoa(page), path, base) //nolint:gosec
	render.Stderr = &renderErr
	if err := render.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("pdftoppm не найден — нужен poppler-utils: %w", err)
		}
		msg := firstLine(renderErr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("pdftoppm: %s", msg)
	}
	pages, err := filepath.Glob(base + "-*.png")
	if err != nil || len(pages) == 0 {
		return "", fmt.Errorf("pdftoppm не выдал изображение страницы %d", page)
	}

	client := gosseract.NewClient()
	text, ocrErr := ocrImage(client, pages[0])
	closeErr := client.Close()
	if ocrErr != nil {
		return "", ocrErr
	}
	if closeErr != nil {
		return "", fmt.Errorf("закрытие OCR-движка: %w", closeErr)
	}
	return text, nil
}

// ocrImage прогонит один PNG через Tesseract.
func ocrImage(client *gosseract.Client, image string) (string, error) {
	if err := client.SetImage(image); err != nil {
		return "", fmt.Errorf("tesseract читает изображение: %w", err)
	}
	if err := client.SetLanguage(ocrLanguages...); err != nil {
		return "", fmt.Errorf("языки OCR: %w", err)
	}
	text, err := client.Text()
	if err != nil {
		return "", fmt.Errorf("tesseract: %w", err)
	}
	return text, nil
}

// nonSpaceRunes — число символов, не считающихся пробельными (Unicode-пробелы
// включительно с \u00a0 — неразрывным).
func nonSpaceRunes(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// firstLine — первая непустая строка вывода (для коротких сообщений об ошибке).
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
