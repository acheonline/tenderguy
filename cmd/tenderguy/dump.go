package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"tenderguy/internal/reqs"
	"tenderguy/internal/scanner"
	"tenderguy/internal/trace"
)

// dumpFileName — файл результатов парсинга P1.8 в каталоге --output-dir.
const dumpFileName = "docs.json"

// dumpRecord — одна запись docs.json: все извлечённые данные файла обхода.
// Записей ровно столько, сколько файлов вернул scanner.Scan, — неподдерживаемые
// тоже здесь: у них пустые text_content и fragments, а причина в note.
// Формы путей в записи сознательно разные. file_path — относительный от
// --input-dir: абсолютный путь зависит от машины, а приёмку сверяют по
// структуре каталога заявки. trace.Fragment.Ref.File во fragments и path
// в failed сохраняют путь ровно так, как его передал оператор через
// --input-dir (при абсолютном входе — абсолютным): они приходят из
// documentFragments и reqs.Extract по контракту закрытой P1.6 — trace
// хранит путь как передан вызывающему. Релятивизировать их здесь значит
// регрессировать P1.6 и разойтись с адресами, которые уже печатают команды
// trace и extract-reqs, — поэтому это не баг, а согласованность с P1.6.
type dumpRecord struct {
	FilePath    string           `json:"file_path"`
	DocType     string           `json:"doc_type"`
	TextContent string           `json:"text_content"`
	Metadata    dumpMetadata     `json:"metadata"`
	Fragments   []trace.Fragment `json:"fragments"`

	// Note — причина непарсинга: у неподдерживаемого файла problem или note
	// обхода, у неразобранного поддерживаемого — «разбор не выполнен».
	Note string `json:"note,omitempty"`

	// Failed — ошибки разбора этого файла: массив рядом с записью, чтобы
	// docs.json оставался массивом записей, а не смешивал уровни.
	Failed []failedDoc `json:"failed,omitempty"`
}

// dumpMetadata — реквизиты записи: inn собирает обе длины ИНН, kpp и dates —
// соответствующие виды карточки P1.5. Полный Card не дублируем: текст и
// адреса значений уже лежат в text_content и fragments.
type dumpMetadata struct {
	INN   []string `json:"inn"`
	KPP   []string `json:"kpp"`
	Dates []string `json:"dates"`
}

// newDumpMetadata — пустые, но незамолчанные списки: в docs.json inn/kpp/dates
// есть всегда, приёмка различает «нет значений» и «поля забыли».
func newDumpMetadata() dumpMetadata {
	return dumpMetadata{INN: []string{}, KPP: []string{}, Dates: []string{}}
}

// dumpCmd сохраняет результаты разбора каталога в docs.json (P1.8): по записи
// на каждый файл обхода — текст из фрагментов P1.2—P1.4, реквизиты карточки
// P1.5 и адреса фрагментов P1.6. Коды выхода те же, что у trace и extract-reqs:
// 0 — всё разобрано, 1 — хотя бы один поддерживаемый документ не разобран или
// не удалось записать файл, 2 — ошибка в флагах (проверяет checkFlags),
// 3 — неверный путь. Каталог без поддерживаемых документов — не ошибка: пишем
// массив записей непарсируемых файлов и выходим с 0.
func dumpCmd(p *params, stdout, stderr io.Writer) int {
	res, err := scanner.Scan(p.inputDir)
	if err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitBadPath
	}

	records := make([]dumpRecord, 0, len(res.Entries))
	undone := 0
	for _, e := range res.Entries {
		rec := dumpRecord{
			FilePath:  e.RelPath,
			DocType:   string(e.Kind),
			Metadata:  newDumpMetadata(),
			Fragments: []trace.Fragment{},
		}
		if !e.Supported {
			rec.Note = skipNote(e)
			records = append(records, rec)
			continue
		}
		frags, err := documentFragments(e.Path, string(e.Kind))
		if err != nil {
			rec.Note = "разбор не выполнен"
			rec.Failed = []failedDoc{{Path: e.Path, Error: err.Error()}}
			undone++
			records = append(records, rec)
			continue
		}
		rec.Fragments = frags
		texts := make([]string, len(frags))
		for i, frag := range frags {
			texts[i] = frag.Text
		}
		rec.TextContent = strings.Join(texts, "\n")
		rec.Metadata = dumpMetaOf(reqs.Extract(e.Path, string(e.Kind), frags))
		records = append(records, rec)
	}

	outPath := filepath.Join(p.outputDir, dumpFileName)
	if err := writeDumpJSON(outPath, records); err != nil {
		fmt.Fprintf(stderr, "tenderguy: %v\n", err)
		return exitProblems
	}
	fmt.Fprintf(stdout, "Записей в %s: %d (поддерживаемых %d, не разобрано %d)\n",
		outPath, len(records), res.Supported, undone)

	if undone > 0 {
		fmt.Fprintf(stderr, "tenderguy: поддерживаемых документов не разобрано %d — см. failed в записях docs.json\n", undone)
		return exitProblems
	}
	return exitOK
}

// skipNote — причина, по которой файл не разбирается: сначала problem обхода
// (файл повреждён, ссылка), иначе классификационное note (archive, service).
func skipNote(e scanner.Entry) string {
	if e.Problem != "" {
		return e.Problem
	}
	return e.Note
}

// dumpMetaOf — метаданные записи из карточки реквизитов: только inn (KindINN10
// и KindINN12), kpp и dates, в порядке первого появления. Повторы значений
// схлопнуты уже в reqs.Extract (Card.add сливает их по виду и значению), так
// что appendUnique здесь — защитная гарантия неизменности docs.json, а не
// единственный механизм дедупликации.
func dumpMetaOf(card *reqs.Card) dumpMetadata {
	m := newDumpMetadata()
	for _, it := range card.Items {
		switch it.Kind {
		case reqs.KindINN10, reqs.KindINN12:
			m.INN = appendUnique(m.INN, it.Value)
		case reqs.KindKPP:
			m.KPP = appendUnique(m.KPP, it.Value)
		case reqs.KindDate:
			m.Dates = appendUnique(m.Dates, it.Value)
		}
	}
	return m
}

// appendUnique — дописывает значение, если такого ещё нет: порядок первого
// появления сохраняется.
func appendUnique(list []string, value string) []string {
	for _, have := range list {
		if have == value {
			return list
		}
	}
	return append(list, value)
}

// writeDumpJSON пишет массив записей docs.json по пути из --output-dir:
// каталог создаётся, если его нет, сериализация как в writeTraceJSON —
// отступы два пробела, HTML не экранируется (пути и названия с кириллицей).
func writeDumpJSON(path string, records []dumpRecord) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("не удалось создать каталог %s: %w", dir, err)
	}
	f, err := os.Create(path) //nolint:gosec // путь собран из флага --output-dir
	if err != nil {
		return fmt.Errorf("не удалось создать %s: %w", path, err)
	}
	if err := encodeDumpJSON(f, records); err != nil {
		if closeErr := f.Close(); closeErr != nil {
			return fmt.Errorf("%v; не удалось закрыть %s: %w", err, path, closeErr)
		}
		return err
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("не удалось закрыть %s: %w", path, err)
	}
	return nil
}

func encodeDumpJSON(w io.Writer, records []dumpRecord) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(records); err != nil {
		return fmt.Errorf("сериализация %s: %w", dumpFileName, err)
	}
	return nil
}
