package rules

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"tenderguy/internal/trace"
)

// Класс правил «состав пакета» (P2.4): статус «есть/нет» по каждому пункту
// требуемого состава из раздела composition конфигурации и замечания по
// итогам сопоставления файлов пакета с пунктами.
//
// Уровни замечаний фиксированы критериями #59 и не берутся из severity
// правила конфига (в отличие от внутренней консистентности класса «reqs»):
//
//   - пропуск обязательного документа — всегда blocking «нет документа в
//     составе»: подавать пакет без обязательного документа нельзя;
//   - неоднозначность (два файла претендуют на один пункт) — всегда warning:
//     движок не выбирает первый молча, решение принимает специалист;
//   - неожиданный файл, не сопоставленный ни с одним пунктом, — всегда
//     warning, а не молчаливый пропуск;
//   - необязательный пункт без файлов — только статус «нет» в таблице.
const ClassComposition Class = "composition"

// CompositionStatus — строка таблицы статусов требуемого состава: по одному
// на пункт конфигурации, порядок строк равен порядку пунктов. Таблица —
// машинно-читаемый итог класса для сведения с P4 (этап подачи заявки).
type CompositionStatus struct {
	// Item — id пункта из конфигурации.
	Item string `json:"item"`
	// Title — наименование требуемого документа.
	Title string `json:"title"`
	// Required — признак обязательности пункта.
	Required bool `json:"required"`
	// Present — статус «есть»: пункту сопоставлен хотя бы один файл.
	Present bool `json:"present"`
	// Files — RelPath сопоставленных файлов, упорядочены.
	Files []string `json:"files,omitempty"`
}

// NewCompositionHandler создаёт обработчик класса «composition» (P2.4).
// Пункты требуемого состава и путь к файлу правил передаёт вызывающий код:
// Handler получает только правило и пакет, поэтому items и rulesPath
// замыкаются в обработчик при регистрации (образец — NewReqsHandler), а
// rulesPath становится адресом пункта требования в замечаниях. table —
// приёмник таблицы статусов: обработчик перезаписывает его при каждом вызове
// (не дописывает), и cmd кладёт таблицу в отчёт после Run.
func NewCompositionHandler(items []CompositionItem, rulesPath string, table *[]CompositionStatus) Handler {
	return func(rule Rule, pkg Package) ([]Finding, error) {
		rows := make([]CompositionStatus, 0, len(items))
		for _, item := range items {
			rows = append(rows, CompositionStatus{
				Item:     item.ID,
				Title:    item.Title,
				Required: item.Required,
			})
		}
		if table != nil {
			*table = rows
		}
		if len(items) == 0 {
			// Требуемый состав не задан: сопоставлять не с чем, а объявлять
			// каждый файл пакета «неожиданным» — значит засыпать отчёт
			// предупреждениями без информации.
			return []Finding{}, nil
		}

		matched, err := matchComposition(items, pkg)
		if err != nil {
			return nil, err
		}
		out := []Finding{}
		covered := make(map[string]bool)
		for i, item := range items {
			files := matched[i]
			rows[i].Present = len(files) > 0
			rows[i].Files = files
			for _, f := range files {
				covered[f] = true
			}
			if len(files) == 0 {
				if item.Required {
					out = append(out, missingCompositionFinding(rule, item, rulesPath, pkg))
				}
				continue
			}
			if len(files) >= 2 {
				out = append(out, ambiguousCompositionFinding(rule, item, files, pkg))
			}
		}
		// Неожиданные файлы — в порядке обхода пакета.
		for _, f := range pkg.Files {
			if covered[f.RelPath] {
				continue
			}
			if finding, ok := unexpectedFileFinding(rule, f, pkg); ok {
				out = append(out, finding)
			}
		}
		return out, nil
	}
}

// matchComposition сопоставляет файлы пакета с пунктами: для каждого пункта —
// упорядоченные RelPath файлов. Сопоставление устойчиво к регистру и пробелам
// в именах (критерий 5 #59).
func matchComposition(items []CompositionItem, pkg Package) ([][]string, error) {
	matched := make([][]string, len(items))
	for i, item := range items {
		seen := make(map[string]bool)
		var files []string
		add := func(rel string) {
			if rel == "" || seen[rel] {
				return
			}
			seen[rel] = true
			files = append(files, rel)
		}
		switch item.Match.By {
		case matchByFilename:
			pattern := normalizeFileName(item.Match.Pattern)
			// Без «*» паттерн сравнивается буквально: скобки и вопросительный
			// знак в именах файлов («договор [1].docx») — обычные символы,
			// а не glob-метасимволы.
			glob := strings.Contains(pattern, "*")
			for _, f := range pkg.Files {
				name := normalizeFileName(f.Name)
				if !glob {
					if name == pattern {
						add(f.RelPath)
					}
					continue
				}
				ok, err := filepath.Match(pattern, name)
				if err != nil {
					// badGlobPattern в LoadConfig ловит только незакрытую
					// символьную группу «[», поэтому битый паттерн вида
					// «*[a-]» доходит до сопоставления. Это опечатка
					// оператора, а не сбой программы: ошибка несёт
					// ErrConfig, и CLI возвращает код 2 (exitUsage).
					return nil, configErr("пункт состава %q: неверный glob %q: %v", item.ID, pattern, err)
				}
				if ok {
					add(f.RelPath)
				}
			}
		case matchByKind:
			for _, f := range pkg.Files {
				if f.Kind == item.Match.Kind {
					add(f.RelPath)
				}
			}
		case matchByRequisite:
			for _, doc := range pkg.Docs {
				if docHasRequisite(doc, item.Match.Kind) {
					add(doc.RelPath)
				}
			}
		}
		sort.Strings(files)
		matched[i] = files
	}
	return matched, nil
}

// normalizeFileName приводит имя файла или паттерн к форме сравнения:
// Unicode-lowercase и один пробел между словами вместо серий пробелов и
// краевых пробелов.
func normalizeFileName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// docHasRequisite — в карточке документа есть хотя бы одно значение данного
// вида. Валидность значения не проверяется: это работа класса «reqs», здесь
// важен факт наличия.
func docHasRequisite(doc Doc, kind string) bool {
	if doc.Reqs == nil {
		return false
	}
	for _, it := range doc.Reqs.Items {
		if it.Kind == kind {
			return true
		}
	}
	return false
}

// missingCompositionFinding — блокирующее замечание «нет документа в составе»
// (критерий 3 #59). Адреса: пункт требования в файле правил — всегда, и
// строка описи пакета, если наименование пункта встречается в тексте
// разобранных документов («где применимо»).
func missingCompositionFinding(rule Rule, item CompositionItem, rulesPath string, pkg Package) Finding {
	f := Finding{
		Rule:     rule.ID,
		Class:    rule.Class,
		Severity: SeverityBlocking,
		Text:     fmt.Sprintf("нет документа в составе: %s (пункт %s)", item.Title, item.ID),
		Refs:     []trace.Ref{{File: rulesPath, Item: item.ID}},
	}
	if ref, quote, ok := findTitleFragment(pkg.Docs, item.Title); ok {
		f.Refs = append([]trace.Ref{ref}, f.Refs...)
		f.Quote = quote
	}
	return f
}

// findTitleFragment ищет наименование пункта в тексте разобранных документов
// (обычно — в описи пакета): сравнение регистронезависимо и устойчиво к
// пробелам. Возвращает адрес первого трассируемого вхождения (RefAt по
// смещению строки) и саму строку как цитату. Вхождение в нетрассируемом
// фрагменте пропускается: адрес замечания обязан открывать место в источнике.
func findTitleFragment(docs []Doc, title string) (trace.Ref, string, bool) {
	normTitle := normalizeFileName(title)
	if normTitle == "" {
		return trace.Ref{}, "", false
	}
	for _, doc := range docs {
		for _, frag := range doc.Fragments {
			offset := 0
			for _, line := range strings.Split(frag.Text, "\n") {
				if strings.Contains(normalizeFileName(line), normTitle) {
					if ref := frag.RefAt(offset); ref.Traced() {
						return ref, strings.TrimSpace(line), true
					}
				}
				offset += len(line) + 1
			}
		}
	}
	return trace.Ref{}, "", false
}

// ambiguousCompositionFinding — предупреждение о неоднозначности (критерий 5
// #59): на один пункт претендует несколько файлов, движок не выбирает первый
// молча. Адреса — по одному на каждый файл, с потолком maxFindingRefs.
func ambiguousCompositionFinding(rule Rule, item CompositionItem, files []string, pkg Package) Finding {
	refs := make([]trace.Ref, 0, len(files))
	for _, rel := range files {
		if len(refs) >= maxFindingRefs {
			break
		}
		refs = append(refs, filePackageRef(pkg, rel))
	}
	return Finding{
		Rule:     rule.ID,
		Class:    rule.Class,
		Severity: SeverityWarning,
		Text: fmt.Sprintf("с пунктом требуемого состава %s (%s) сопоставлено несколько файлов: %s — какой из них верный, решает специалист",
			item.ID, item.Title, strings.Join(files, ", ")),
		Refs: refs,
	}
}

// unexpectedFileFinding — предупреждение о файле, не сопоставленном ни с
// одним пунктом требуемого состава (критерий 4 #59). Второе значение false
// означает, что у файла нет ни одного трассируемого адреса и замечание
// пришлось пропустить: инвариант адресности важнее полноты (образец —
// пропуски в классе «reqs»).
func unexpectedFileFinding(rule Rule, f File, pkg Package) (Finding, bool) {
	ref := filePackageRef(pkg, f.RelPath)
	if !ref.Traced() {
		return Finding{}, false
	}
	return Finding{
		Rule:     rule.ID,
		Class:    rule.Class,
		Severity: SeverityWarning,
		Text:     fmt.Sprintf("файл не сопоставлен ни с одним пунктом требуемого состава: %s", f.RelPath),
		Refs:     []trace.Ref{ref},
	}, true
}

// filePackageRef — адрес файла пакета: у разобранного документа — первое
// трассируемое место (docRef), у неразобранного — файл целиком через
// trace.Ref.Item. Пустой RelPath даёт нетрассируемый адрес: вызывающий код
// это проверяет.
func filePackageRef(pkg Package, rel string) trace.Ref {
	for _, doc := range pkg.Docs {
		if doc.RelPath != rel {
			continue
		}
		if ref, ok := docRef(doc); ok {
			return ref
		}
		break
	}
	return trace.Ref{File: rel, Item: "файл пакета"}
}
