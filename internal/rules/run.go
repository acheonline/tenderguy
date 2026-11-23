package rules

import (
	"fmt"
	"sort"
	"strings"
)

// Class — имя класса правил: набора проверок одной природы (сверка
// реквизитов, состав пакета, сроки «свежести»). Классы регистрирует вызывающий
// код, а движок по имени только находит обработчик — смысла проверок он не
// знает, поэтому добавить новый класс можно без правки движка.
type Class string

// Handler — проверка одного правила: получает правило из конфига и пакет,
// возвращает замечания. Ошибка обработчика означает сбой самой проверки, а не
// замечание по пакету, и обрывает прогон.
type Handler func(Rule, Package) ([]Finding, error)

// Runner — реестр классов правил и прогон конфига по пакету. Нулевое значение
// пригодно к работе: Register заводит карту сам, отдельный конструктор не
// нужен — в P2.1 движок создаётся пустым.
type Runner struct {
	handlers map[Class]Handler
}

// Register объявляет обработчик класса правил. Повторная регистрация заменяет
// обработчик: класс на движок один, иначе правила одного класса выполнялись бы
// двумя проверками в непредсказуемом порядке.
func (r *Runner) Register(class Class, handler Handler) {
	if r.handlers == nil {
		r.handlers = make(map[Class]Handler)
	}
	r.handlers[class] = handler
}

// Classes — зарегистрированные классы в алфавитном порядке. Список нужен
// сообщению об ошибке конфига (оператор обязан видеть, какие классы вообще
// существуют) и тесту инварианта адресности, который проходит по всем классам,
// не зная их имён заранее.
func (r *Runner) Classes() []Class {
	out := make([]Class, 0, len(r.handlers))
	for class := range r.handlers {
		out = append(out, class)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Report — итог прогона правил по пакету.
type Report struct {
	InputDir   string `json:"input_dir"`
	RulesTotal int    `json:"rules_total"`
	Blocking   int    `json:"blocking"`
	Warnings   int    `json:"warnings"`

	// Findings и Failed в JSON присутствуют всегда и пусты как [], а не null:
	// приёмка различает «значений нет» и «поле забыли» (свойство dump-docs).
	Findings []Finding `json:"findings"`
	Failed   []Failed  `json:"failed"`

	// Note — пояснение к отчёту, когда проверять нечего (в конфиге нет
	// правил). В остальных случаях поле пусто и в JSON не попадает.
	Note string `json:"note,omitempty"`

	// Composition — таблица статусов требуемого состава (P2.4). Runner её не
	// заполняет: движок не знает смысла классов, таблицу отдаёт обработчик
	// через приёмник при регистрации, а в отчёт её кладёт cmd после Run.
	Composition []CompositionStatus `json:"composition,omitempty"`
}

// Run применяет правила конфига к пакету и считает отчёт. Порядок правил
// конфига сохраняется в отчёте: замечания читают по списку, а не ищут их.
// Ошибки делятся на два вида — неверный конфиг (несёт ErrConfig, код выхода 2)
// и сбой проверки или отсутствие адреса у замечания (внутренняя ошибка, код 1).
func (r *Runner) Run(pkg Package, cfg Config) (Report, error) {
	if err := r.checkClasses(cfg); err != nil {
		return Report{}, err
	}

	rep := Report{
		InputDir:   pkg.Root,
		RulesTotal: len(cfg.Rules),
		Findings:   []Finding{},
		Failed:     pkg.Failed,
	}
	if rep.Failed == nil {
		rep.Failed = []Failed{}
	}

	for i, rule := range cfg.Rules {
		where := fmt.Sprintf("правило #%d (id %q, класс %q)", i+1, rule.ID, rule.Class)
		found, err := r.handlers[rule.Class](rule, pkg)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", where, err)
		}
		for _, f := range found {
			// Замечание без адреса — программная ошибка класса правил, а не
			// свойство пакета: критерий эпика требует ссылку на источник у
			// 100 % замечаний, поэтому прогон обрывается, а не продолжается
			// с непоказуемым результатом.
			if err := f.Validate(); err != nil {
				return Report{}, fmt.Errorf("%s: %w", where, err)
			}
			rep.Findings = append(rep.Findings, f)
			switch f.Severity {
			case SeverityBlocking:
				rep.Blocking++
			case SeverityWarning:
				rep.Warnings++
			}
		}
	}
	return rep, nil
}

// checkClasses — все ли классы конфига зарегистрированы в движке.
func (r *Runner) checkClasses(cfg Config) error {
	for i, rule := range cfg.Rules {
		if _, ok := r.handlers[rule.Class]; ok {
			continue
		}
		return configErr("правило #%d (id %q): поле class = %q неизвестно движку%s",
			i+1, rule.ID, rule.Class, r.classesHint())
	}
	return nil
}

// classesHint — пояснение к ошибке неизвестного класса. В P2.1 движок
// поставляется без зарегистрированных классов, поэтому сообщение обязано
// объяснять причину и откуда классы возьмутся, а не выглядеть поломкой.
func (r *Runner) classesHint() string {
	classes := r.Classes()
	if len(classes) == 0 {
		return " — зарегистрированных классов нет: их добавляют задачи P2.3—P2.5"
	}
	names := make([]string, len(classes))
	for i, class := range classes {
		names[i] = string(class)
	}
	return " — зарегистрированные классы: " + strings.Join(names, ", ")
}
