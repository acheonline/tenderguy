package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// Значения для CLI-тестов те же, что в internal/reqs, но объявлены заново:
// пакет main не видит неэкспортируемые константы тестов внутреннего пакета.
const (
	cliINN     = "7714567895" // корректный ИНН (контрольная цифра сходится)
	cliINNBad  = "7714567894" // тот же ИНН со сбитой контрольной цифрой
	cliKPP     = "781401001"  // КПП заказчика из пакета ТЭК СПб
	cliOGRN    = "1027700014831"
	cliDate    = "01.02.2026"
	cliMoney   = "5 000 000,00 руб."
	cliTextDoc = "Участник закупки: ИНН " + cliINN + ", КПП " + cliKPP +
		"\nОГРН " + cliOGRN + "\nНМЦК " + cliMoney + "\nДата предложения " + cliDate
)

// reqsDir возвращает каталог с документом, полным реквизитов, документом без
// реквизитов и документом, который не открывается как .docx.
func reqsDir(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeDocxFile(t, dir, "анкета.docx", textParagraph(cliTextDoc))
	writeDocxFile(t, dir, "памятка.docx", textParagraph("Памятка участника: читать внимательно"))
	writeDocxParts(t, dir, "без-связей.docx", false, textParagraph("ИНН "+cliINN))
	return dir
}

func TestRunExtractReqs(t *testing.T) {
	dir := reqsDir(t)
	full := filepath.Join(dir, "анкета.docx")
	noreqs := filepath.Join(dir, "памятка.docx")
	pdfName := writePdfFile(t, t.TempDir(), "заявка.pdf", cliPdfTextFixture)

	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantOut    []string
		wantErrOut []string
		noOut      bool
	}{
		{
			name:     "один документ, карточка с реквизитами",
			args:     []string{"--command", "extract-reqs", "--file", full},
			wantExit: exitOK,
			wantOut: []string{
				"Карточек: 1 (с реквизитами 1), файлов без карточки 0, подозрительных значений 0",
				"анкета.docx (docx)",
				"ИНН(10) " + cliINN,
				"КПП " + cliKPP,
				"ОГРН " + cliOGRN,
				"дата 2026-02-01",
				"сумма 5000000.00 RUB",
			},
		},
		{
			name:     "документ без реквизитов — пустая карточка с замечанием",
			args:     []string{"--command", "extract-reqs", "--file", noreqs},
			wantExit: exitOK,
			wantOut: []string{
				"реквизитов не найдено",
				"Замечание: ИНН/ОГРН не найдены",
			},
		},
		{
			name:     "подозрительное значение помечено и объявлено в stderr",
			args:     []string{"--command", "extract-reqs", "--file", writeDocxFile(t, t.TempDir(), "заявка.docx", textParagraph("ИНН "+cliINNBad))},
			wantExit: exitOK,
			wantOut:  []string{"✗ ИНН(10) " + cliINNBad + " [метка: ИНН] — контрольная цифра не сходится"},
			wantErrOut: []string{
				"подозрительных значений 1",
			},
		},
		{
			name:     "несколько форм из каталога",
			args:     []string{"--command", "extract-reqs", "--input-dir", dir},
			wantExit: exitProblems,
			wantOut: []string{
				"Карточек: 2",
				"файлов без карточки 1",
				"Ошибки разбора:",
				"без-связей.docx —",
			},
			wantErrOut: []string{"файлов без карточки 1"},
		},
		{
			name:     "pdf: карточка строится по текстовому слою",
			args:     []string{"--command", "extract-reqs", "--file", pdfName},
			wantExit: exitOK,
			wantOut: []string{
				"заявка.pdf (pdf)",
				"реквизитов не найдено",
				"Замечание: ИНН/ОГРН не найдены",
			},
		},
		{
			name:       "файл неподдерживаемого формата",
			args:       []string{"--command", "extract-reqs", "--file", filepath.Join(dir, "памятка.txt")},
			wantExit:   exitBadPath,
			wantErrOut: []string{"формат файла", "не поддерживается"},
			noOut:      true,
		},
		{
			name:       "не задан вход",
			args:       []string{"--command", "extract-reqs"},
			wantExit:   exitUsage,
			wantErrOut: []string{"не задан вход"},
			noOut:      true,
		},
		{
			name:       "file и input-dir вместе",
			args:       []string{"--command", "extract-reqs", "--file", full, "--input-dir", dir},
			wantExit:   exitUsage,
			wantErrOut: []string{"выберите что-то одно"},
			noOut:      true,
		},
		{
			name:       "--with-text карточке не нужен",
			args:       []string{"--command", "extract-reqs", "--file", full, "--with-text"},
			wantExit:   exitUsage,
			wantErrOut: []string{"--with-text не используется в extract-reqs"},
			noOut:      true,
		},
		{
			name:       "справка перечисляет extract-reqs",
			args:       []string{"--command", "ocr", "--file", full},
			wantExit:   exitUsage,
			wantErrOut: []string{`неизвестный --command "ocr"`, "extract-reqs"},
			noOut:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if got := run(tc.args, &out, &errOut); got != tc.wantExit {
				t.Errorf("код выхода = %d, хотели %d\nstdout:\n%s\nstderr:\n%s", got, tc.wantExit, out.String(), errOut.String())
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(out.String(), want) {
					t.Errorf("в stdout нет %q\n%s", want, out.String())
				}
			}
			for _, want := range tc.wantErrOut {
				if !strings.Contains(errOut.String(), want) {
					t.Errorf("в stderr нет %q\n%s", want, errOut.String())
				}
			}
			if tc.noOut && out.Len() > 0 {
				t.Errorf("stdout должен быть пуст, получили\n%s", out.String())
			}
		})
	}
}

func TestRunExtractReqsJSONDecodes(t *testing.T) {
	full := writeDocxFile(t, t.TempDir(), "анкета.docx", textParagraph(cliTextDoc))

	var out, errOut bytes.Buffer
	if code := run([]string{"--command", "extract-reqs", "--file", full, "--format", "json"}, &out, &errOut); code != exitOK {
		t.Fatalf("код выхода = %d, stderr: %s", code, errOut.String())
	}

	var rep struct {
		Cards []struct {
			Name    string `json:"name"`
			Format  string `json:"format"`
			Total   int    `json:"total"`
			Suspect int    `json:"suspect"`
			Items   []struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
				Valid bool   `json:"valid"`
				Hits  int    `json:"hits"`
			} `json:"items"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("отчёт не читается как json: %v\n%s", err, out.String())
	}
	if len(rep.Cards) != 1 {
		t.Fatalf("карточек %d, хотим 1", len(rep.Cards))
	}
	card := rep.Cards[0]
	if card.Name != "анкета.docx" || card.Format != "docx" {
		t.Errorf("карточка: %q %q", card.Name, card.Format)
	}
	if card.Total != len(card.Items) || card.Suspect != 0 {
		t.Errorf("Total=%d при %d значениях, Suspect=%d", card.Total, len(card.Items), card.Suspect)
	}
	wantKinds := map[string]string{
		"inn10": cliINN,
		"kpp":   cliKPP,
		"ogrn":  cliOGRN,
		"date":  "2026-02-01",
		"money": "5000000.00 RUB",
	}
	for kind, value := range wantKinds {
		found := false
		for _, it := range card.Items {
			if it.Kind == kind && it.Value == value {
				found = true
				if !it.Valid {
					t.Errorf("%s=%s помечено подозрительным", kind, value)
				}
			}
		}
		if !found {
			t.Errorf("в json нет %s=%s: %+v", kind, value, card.Items)
		}
	}
}

// Карточка должна сливать повторы одного значения: в заявке ИНН встречается в
// нескольких формах на одной странице, а сверять P2 будет значение, а не строки.
func TestRunExtractReqsMergesRepeats(t *testing.T) {
	file := writeDocxFile(t, t.TempDir(), "заявка.docx",
		textParagraph("ИНН "+cliINN)+textParagraph("Подпись: ИНН "+cliINN))

	var out, errOut bytes.Buffer
	if code := run([]string{"--command", "extract-reqs", "--file", file}, &out, &errOut); code != exitOK {
		t.Fatalf("код выхода = %d, stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "ИНН(10) "+cliINN+" ×2") {
		t.Errorf("повтор не отмечён ×2:\n%s", out.String())
	}
	if n := strings.Count(out.String(), "ИНН(10) "+cliINN); n != 1 {
		t.Errorf("значение ИНН выведено %d раз, хотим 1:\n%s", n, out.String())
	}
}

func TestExtractReqsUsageMentionedInHelp(t *testing.T) {
	var buf bytes.Buffer
	fs, _ := newFlagSet(&buf)
	usage(fs, &buf)
	for _, want := range []string{"extract-reqs", "--file", "--input-dir", "Коды выхода"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("в справке нет %q:\n%s", want, buf.String())
		}
	}
}
