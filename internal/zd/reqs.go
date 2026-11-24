package zd

import (
	"fmt"
	"strings"

	"tenderguy/internal/trace"
)

// Извлечение требований к участнику закупки (P2.8): границы раздела
// «Требования, предъявляемые к участникам закупки» берутся из оглавления,
// каждый непустой абзац раздела — требование; категория определяется по
// карте ключевых слов. Абзац, не попавший ни в одну категорию, получает
// «прочее» и не отбрасывается: потеря требования опаснее лишней строки.

// ReqItem — одно требование к участнику.
type ReqItem struct {
	Text     string `json:"text"`
	Category string `json:"category"`
	// Ref — адрес абзаца в документе; Traced() обязан быть истинным.
	Ref trace.Ref `json:"ref"`
}

// RequirementsResult — итог извлечения требований к участнику.
type RequirementsResult struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Section — заголовок раздела, из которого извлечены требования.
	Section string    `json:"section,omitempty"`
	Items   []ReqItem `json:"items,omitempty"`
}

// reqCategory — категория требований и её ключевые слова (сравниваются по
// нормализованному тексту абзаца; первое совпадение побеждает, поэтому
// порядок значим: «лицензия на вид деятельности» важнее общего «право»).
type reqCategory struct {
	name     string
	keywords []string
}

var reqCategories = []reqCategory{
	{"лицензии и право на вид деятельности", []string{"лицензи", "право на осуществление", "видов деятельности", "вид деятельности", "аккредитац"}},
	{"финансовая устойчивость", []string{"финансов", "задолженност", "налог", "бухгалтерск"}},
	{"правоспособность", []string{"правоспособн", "дееспособн", "ликвидац", "банкрот", "реорганизац", "егрюл", "егрип"}},
	{"квалификация", []string{"квалификац", "опыт", "техническая часть"}},
	{"деловая репутация", []string{"репутац"}},
	{"обеспечение исполнения договора", []string{"обеспечен", "аванс"}},
	{"кадры", []string{"кадр", "штат", "работник", "персонал"}},
	{"аффилированность", []string{"аффилирован", "взаимозависим"}},
	{"судебные разбирательства", []string{"судебн", "разбирательств", "арбитраж"}},
}

// categoryOther — категория абзаца, который не попал ни в одну из карты.
const categoryOther = "прочее"

// extractRequirements собирает требования из раздела «Требования,
// предъявляемые к участникам закупки». Раздел ищется по оглавлению; нет
// оглавления или раздела — not_found с причиной, требования не выдумываются.
func extractRequirements(x *extractor) RequirementsResult {
	sec, ok := findReqsSection(x.sections)
	if !ok {
		return RequirementsResult{
			Status: StatusNotFound,
			Reason: "раздел «Требования, предъявляемые к участникам закупки» не найден: оглавление отсутствует или заголовок раздела не совпал с телом",
		}
	}
	res := RequirementsResult{Status: StatusFull, Section: sec.Title}
	for _, b := range x.doc.Blocks {
		if b.Table != nil || b.Index <= sec.StartPara || b.Index >= sec.EndPara {
			continue
		}
		text := strings.TrimSpace(b.Text)
		if text == "" {
			continue
		}
		res.Items = append(res.Items, ReqItem{
			Text:     text,
			Category: categorize(text),
			Ref:      x.ref(b.Index, b.Page, ""),
		})
	}
	if len(res.Items) == 0 {
		res.Status = StatusPartial
		res.Reason = fmt.Sprintf("раздел «%s» найден (абз. %d), но не содержит ни одного непустого абзаца", sec.Title, sec.StartPara)
	}
	return res
}

// findReqsSection — раздел требований к участнику: заголовок содержит и
// «требован», и «участник».
func findReqsSection(sections []Section) (Section, bool) {
	for _, s := range sections {
		n := normText(s.Title)
		if strings.Contains(n, "требован") && strings.Contains(n, "участник") {
			return s, true
		}
	}
	return Section{}, false
}

// categorize относит абзац к категории по карте ключевых слов.
func categorize(text string) string {
	n := normText(text)
	for _, c := range reqCategories {
		for _, k := range c.keywords {
			if strings.Contains(n, k) {
				return c.name
			}
		}
	}
	return categoryOther
}
