package rules

import "sort"

// Подготовка отчёта о проверке (P2.6): группировка замечаний по классам и
// критичности, сводка-счётчики по классам и модель непроверенного класса.
// Все функции здесь — чистые «данные в данные»: порядок строк отчёта обязан
// быть детерминированным, чтобы повторный прогон по тому же входу давал тот
// же отчёт (критерий P2.6).

// Skipped — зарегистрированный класс, который не проверялся в этом прогоне,
// и причина. Класс без правил в конфиге не должен молча исчезать из отчёта:
// специалист обязан видеть, какая часть пакета не проверена и почему.
type Skipped struct {
	Class  Class  `json:"class"`
	Reason string `json:"reason"`
}

// ClassGroup — замечания одного класса для вывода в отчёте: имя класса и
// замечания, упорядоченные по критичности (сначала блокирующие).
type ClassGroup struct {
	Class    Class
	Findings []Finding
}

// GroupFindings группирует замечания по классам для отчёта (P2.6). Порядок
// детерминирован: классы — по алфавиту (тот же порядок, что даёт
// Runner.Classes, поэтому сводка и секции замечаний читаются единообразно);
// внутри класса блокирующие замечания идут раньше остальных; внутри одной
// критичности сохраняется порядок входного списка — порядок правил конфига.
// Входной срез не изменяется. Пустой вход даёт nil.
func GroupFindings(findings []Finding) []ClassGroup {
	if len(findings) == 0 {
		return nil
	}
	byClass := make(map[Class][]Finding)
	for _, f := range findings {
		byClass[f.Class] = append(byClass[f.Class], f)
	}
	classes := make([]Class, 0, len(byClass))
	for class := range byClass {
		classes = append(classes, class)
	}
	sort.Slice(classes, func(i, j int) bool { return classes[i] < classes[j] })

	groups := make([]ClassGroup, 0, len(classes))
	for _, class := range classes {
		list := byClass[class]
		ordered := make([]Finding, 0, len(list))
		// Два прохода дают устойчивое разбиение по критичности без sort:
		// сначала блокирующие, затем остальные, порядок внутри части — входной.
		for _, f := range list {
			if f.Severity == SeverityBlocking {
				ordered = append(ordered, f)
			}
		}
		for _, f := range list {
			if f.Severity != SeverityBlocking {
				ordered = append(ordered, f)
			}
		}
		groups = append(groups, ClassGroup{Class: class, Findings: ordered})
	}
	return groups
}

// ClassSummary — счётчики замечаний одного класса для сводки отчёта: всего
// и в разбивке по критичности.
type ClassSummary struct {
	Class    Class `json:"class"`
	Total    int   `json:"total"`
	Blocking int   `json:"blocking"`
	Warnings int   `json:"warnings"`
}

// SummarizeClasses считает сводку по классам (P2.6). Классы упорядочены по
// алфавиту — как в GroupFindings, чтобы сводка и секции замечаний читались в
// одном порядке. Пустой вход даёт nil: сводка без замечаний не нужна, а в
// JSON-отчёте поле summary отсутствует (omitempty).
func SummarizeClasses(findings []Finding) []ClassSummary {
	if len(findings) == 0 {
		return nil
	}
	index := make(map[Class]int)
	out := make([]ClassSummary, 0)
	for _, f := range findings {
		i, ok := index[f.Class]
		if !ok {
			i = len(out)
			index[f.Class] = i
			out = append(out, ClassSummary{Class: f.Class})
		}
		out[i].Total++
		switch f.Severity {
		case SeverityBlocking:
			out[i].Blocking++
		case SeverityWarning:
			out[i].Warnings++
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Class < out[j].Class })
	return out
}
