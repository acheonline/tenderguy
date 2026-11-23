// Package rules — движок детерминированных проверок пакета заявок (P2.1).
//
// Правила здесь — данные, а не код: движок читает JSON-конфиг (флаг --rules),
// находит обработчик по классу правила и применяет его к пакету. Смысла
// конкретных проверок (сверка реквизитов, состав пакета, сроки «свежести»)
// движок не знает — их приносят классы правил задачами P2.3—P2.5.
//
// Главное свойство отчёта — адресность: замечание обязано нести ссылку на
// фрагмент-источник (trace.Ref с Traced()==true), иначе специалист не сможет
// открыть место в документе. Инвариант держат два слоя: Finding.Validate
// проверяет одно замечание (его вызывает тест каждого класса правил), а
// Runner.Run отклоняет замечание без адреса как ошибку класса.
package rules

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"tenderguy/internal/reqs"
	"tenderguy/internal/scanner"
	"tenderguy/internal/trace"
)

// Severity — уровень критичности замечания. Шкала из двух значений (критерий
// P2.1): блокирующее означает, что пакет нельзя подавать, предупреждение —
// повод открыть место руками и решить по обстановке.
type Severity string

const (
	SeverityBlocking Severity = "blocking"
	SeverityWarning  Severity = "warning"
)

// SupportedVersion — единственная версия схемы конфига, которую понимает
// движок. Версия явная, потому что файл правил живёт в репозитории дольше
// кода: конфиг от будущей схемы обязан отказать в работе, а не молча потерять
// незнакомые поля.
const SupportedVersion = 1

// ErrConfig — маркер ошибки содержимого файла правил. По нему вызывающий код
// отличает неверный конфиг (код выхода 2) от недоступного файла (код 3).
var ErrConfig = errors.New("ошибка файла правил")

// configErr собирает ошибку содержимого конфига: она всегда несёт ErrConfig,
// чтобы коды выхода не зависели от того, где именно проверка сработала.
func configErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConfig, fmt.Sprintf(format, args...))
}

// Config — содержимое файла правил. P2.1 определил version и rules, P2.3
// добавляет раздел orgs — эталонные реквизиты юрлиц для класса «reqs», P2.4 —
// раздел composition (требуемый состав пакета); каталог сроков добавит задача
// P2.5. Поэтому неизвестное поле верхнего уровня — ошибка: сегодня оно
// может означать только опечатку в имени существующего.
type Config struct {
	Version     int               `json:"version"`
	Orgs        []Org             `json:"orgs,omitempty"`
	Composition []CompositionItem `json:"composition,omitempty"`
	Rules       []Rule            `json:"rules"`
}

// Способы сопоставления файла с пунктом требуемого состава (P2.4).
const (
	matchByFilename  = "filename"
	matchByKind      = "kind"
	matchByRequisite = "requisite"
)

// CompositionItem — один пункт требуемого состава пакета. Требуемый состав
// задаёт человек через конфигурацию: автоматическое извлечение требований из
// документации закупки — задача P2.8, здесь её нет.
type CompositionItem struct {
	// ID обязателен и уникален: на него ссылается адрес замечания
	// (trace.Ref.Item) и строка таблицы статусов.
	ID string `json:"id"`
	// Title — наименование требуемого документа; по нему пункт ищется в
	// тексте описи пакета для адреса блокирующего замечания.
	Title string `json:"title"`
	// Required — признак обязательности: пропуск обязательного пункта
	// блокирует пакет, необязательного — даёт только статус «нет».
	Required bool `json:"required"`
	// Match — способ сопоставления файлов пакета с пунктом.
	Match CompositionMatch `json:"match"`
}

// CompositionMatch — способ сопоставления: по имени файла (pattern —
// glob filepath.Match или точное имя), по типу документа из набора
// scanner.Kind или по наличию в карточке документа реквизита из набора
// reqs.Kind*. Лишние для выбранного способа поля — ошибка конфига, а не
// молчаливое игнорирование: оператор обязан видеть опечатку.
type CompositionMatch struct {
	By      string `json:"by"`
	Pattern string `json:"pattern,omitempty"`
	Kind    string `json:"kind,omitempty"`
}

// scannerKinds — допустимые типы документов для сопоставления by=kind:
// набор обязан совпадать с классификацией обхода P1.1.
var scannerKinds = []string{
	string(scanner.KindDocx), string(scanner.KindXlsx), string(scanner.KindPdf),
	string(scanner.KindOfficeLegacy), string(scanner.KindArchive),
	string(scanner.KindService), string(scanner.KindOther),
}

// requisiteKinds — виды реквизитов карточки для сопоставления by=requisite.
// Дата и сумма не входят: наличие «какой-то даты» не отличает один документ
// от другого, а идентификаторы и наименование — отличают.
var requisiteKinds = []string{
	reqs.KindINN10, reqs.KindINN12, reqs.KindKPP,
	reqs.KindOGRN, reqs.KindOGRNIP, reqs.KindOrgName, reqs.KindAddress,
}

// Org — эталонные реквизиты одного юрлица (сторона договора, заказчик,
// оператор). Эталон вводит человек через конфигурацию, а не «документ-эталон»
// (решение P0-30); множественность оставлена пересмотром A3 — пакет может
// содержать реквизиты нескольких законных сторон, поэтому Orgs является
// списком. Пустой реквизит означает «не задан» и в сверке не участвует;
// эталон без единого реквизита бессмысленен и отвергается конфигом.
type Org struct {
	// ID обязателен: на него ссылаются замечания и будущие классы правил.
	ID string `json:"id"`
	// Name — наименование юрлица; сравнивается нормализацией P2.2.
	Name string `json:"name,omitempty"`
	// Address — юридический адрес; сравнивается нормализацией P2.2.
	Address string `json:"address,omitempty"`
	// INN, KPP, OGRN — конфигурация допускает пробелы и разделители, но
	// разбор приводит значение к цифровому виду и проверяет его валидаторами
	// reqs: опечатка в эталоне не должна становиться ложным блокирующим
	// замечанием по всему пакету.
	INN  string `json:"inn,omitempty"`
	KPP  string `json:"kpp,omitempty"`
	OGRN string `json:"ogrn,omitempty"`
}

// Rule — одно правило конфига. Движок передаёт правило обработчику класса как
// есть: Params — вход проверки, движок его не разбирает и не понимает, поэтому
// значения остаются сырым JSON.
type Rule struct {
	ID       string                     `json:"id"`
	Class    Class                      `json:"class"`
	Severity Severity                   `json:"severity"`
	Params   map[string]json.RawMessage `json:"params,omitempty"`
}

// Finding — замечание правила: что не так, насколько это критично и где в
// источнике находится место. Quote — цитата фрагмента-источника; она может
// быть пустой, когда цитировать нечего (например, замечание об отсутствии
// документа в составе пакета).
type Finding struct {
	Rule     string      `json:"rule"`
	Class    Class       `json:"class"`
	Severity Severity    `json:"severity"`
	Text     string      `json:"text"`
	Quote    string      `json:"quote,omitempty"`
	Refs     []trace.Ref `json:"refs"`
}

// Validate проверяет инварианты замечания: корректная severity и хотя бы один
// адрес фрагмента-источника. Severity проверяется здесь, а не выводится из
// правила, потому что класс вправе менять уровень по исходу проверки
// (расхождение с эталоном блокирует, отсутствие поля — лишь предупреждение);
// опечатка обработчика обязана стать ошибкой прогона, а не потерянным
// замечанием (контракт, отложенный ревью P2.1). Метод отдельный от Run,
// потому что тест класса правил обязан прогнать его по каждому замечанию
// своей проверки — так же, как TestEveryFragmentHasAddress в P1.6 проходит по
// всем фрагментам парсера.
func (f Finding) Validate() error {
	switch f.Severity {
	case SeverityBlocking, SeverityWarning:
	default:
		return fmt.Errorf("замечание %q: severity %q — допустимы только blocking или warning", f.Text, string(f.Severity))
	}
	for _, ref := range f.Refs {
		if ref.Traced() {
			return nil
		}
	}
	return fmt.Errorf("замечание %q: нет ни одного адреса в источнике — нужен trace.Ref с Traced()==true", f.Text)
}

// rawConfig — конфиг с «сырыми» правилами и эталонами. Правила и эталоны
// разбираются по одному, чтобы сообщение об ошибке называло номер элемента и
// его id: оператор правит файл руками и должен видеть строку, а не только имя
// поля.
type rawConfig struct {
	Version     int               `json:"version"`
	Orgs        []json.RawMessage `json:"orgs"`
	Composition []json.RawMessage `json:"composition"`
	Rules       []json.RawMessage `json:"rules"`
}

// LoadConfig читает файл правил и проверяет его содержимое. Ошибка чтения
// возвращается как есть — это неверный путь (код выхода 3), а ошибки
// содержимого несут ErrConfig (код выхода 2).
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("не удалось прочитать файл правил %s: %w", path, err)
	}

	var raw rawConfig
	// Синтаксис проверяется отдельным проходом: сообщение «битый JSON» обязано
	// означать именно нечитаемый файл, а не опечатку в имени поля или неверный
	// тип значения — их строгий декодер выдаёт отдельной формулировкой.
	var probe json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return Config{}, configErr("битый JSON: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, configErr("структура конфига неверна: %v", err)
	}
	switch {
	case raw.Version == 0:
		return Config{}, configErr("поле version не задано: движок понимает версию %d", SupportedVersion)
	case raw.Version != SupportedVersion:
		return Config{}, configErr("поле version = %d: поддерживается только версия %d", raw.Version, SupportedVersion)
	}

	cfg := Config{Version: raw.Version}
	// Эталоны разбираются до правил: класс «reqs» обязан увидеть их готовыми,
	// а ошибка эталона — назвать номер и id, как ошибка правила.
	seenOrg := make(map[string]int, len(raw.Orgs))
	for i, item := range raw.Orgs {
		org, err := parseOrg(i, item)
		if err != nil {
			return Config{}, err
		}
		if first, dup := seenOrg[org.ID]; dup {
			return Config{}, configErr("эталоны #%d и #%d: одинаковый id %q — эталон обязан быть единственным в конфиге",
				first+1, i+1, org.ID)
		}
		seenOrg[org.ID] = i
		cfg.Orgs = append(cfg.Orgs, org)
	}
	// Требуемый состав разбирается до правил: класс «composition» обязан
	// увидеть его готовым, а ошибка пункта — назвать номер и id, как ошибка
	// эталона и правила.
	seenComp := make(map[string]int, len(raw.Composition))
	for i, item := range raw.Composition {
		comp, err := parseCompositionItem(i, item)
		if err != nil {
			return Config{}, err
		}
		if first, dup := seenComp[comp.ID]; dup {
			return Config{}, configErr("пункты состава #%d и #%d: одинаковый id %q — пункт обязан быть единственным в конфиге",
				first+1, i+1, comp.ID)
		}
		seenComp[comp.ID] = i
		cfg.Composition = append(cfg.Composition, comp)
	}
	// Пустой список правил или его отсутствие — не ошибка, а «проверять
	// нечего»: движок обязан вернуть пустой отчёт, а не падать.
	seen := make(map[string]int, len(raw.Rules))
	for i, item := range raw.Rules {
		rule, err := parseRule(i, item)
		if err != nil {
			return Config{}, err
		}
		if first, dup := seen[rule.ID]; dup {
			return Config{}, configErr("правила #%d и #%d: одинаковый id %q — правило обязано быть единственным в конфиге",
				first+1, i+1, rule.ID)
		}
		seen[rule.ID] = i
		cfg.Rules = append(cfg.Rules, rule)
	}
	return cfg, nil
}

// parseRule разбирает одно правило и проверяет его обязательные поля. Класс
// здесь не проверяется: зарегистрирован он или нет, знает только Runner.
func parseRule(index int, data json.RawMessage) (Rule, error) {
	var rule Rule
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rule); err != nil {
		return Rule{}, configErr("правило #%d: структура правила неверна: %v", index+1, err)
	}
	if strings.TrimSpace(rule.ID) == "" {
		return Rule{}, configErr("правило #%d: поле id обязательно и не может быть пустым", index+1)
	}

	where := fmt.Sprintf("правило #%d (id %q)", index+1, rule.ID)
	if strings.TrimSpace(string(rule.Class)) == "" {
		return Rule{}, configErr("%s: поле class обязательно — по нему движок находит обработчик проверки", where)
	}
	switch rule.Severity {
	case SeverityBlocking, SeverityWarning:
	default:
		if strings.TrimSpace(string(rule.Severity)) == "" {
			return Rule{}, configErr("%s: поле severity обязательно — %s или %s", where, SeverityBlocking, SeverityWarning)
		}
		return Rule{}, configErr("%s: поле severity = %q — допустимы %s или %s",
			where, rule.Severity, SeverityBlocking, SeverityWarning)
	}
	return rule, nil
}

// parseOrg разбирает один эталон и проверяет его реквизиты. Числовые
// реквизиты приводятся к цифрам и проходят те же валидаторы reqs, что и
// значения из документов: эталон с неверной контрольной цифрой превратил бы
// каждое расхождение пакета в ложное блокирующее замечание, поэтому ошибка
// конфигурации ловится на входе.
func parseOrg(index int, data json.RawMessage) (Org, error) {
	var org Org
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&org); err != nil {
		return Org{}, configErr("эталон #%d: структура эталона неверна: %v", index+1, err)
	}

	org.ID = strings.TrimSpace(org.ID)
	if org.ID == "" {
		return Org{}, configErr("эталон #%d: поле id обязательно и не может быть пустым", index+1)
	}
	org.Name = strings.TrimSpace(org.Name)
	org.Address = strings.TrimSpace(org.Address)

	where := fmt.Sprintf("эталон #%d (id %q)", index+1, org.ID)
	// Числовые реквизиты: конфигурация допускает пробелы и дефисы как
	// разделители («7707 0838-93»), но буква внутри — опечатка, а не разделитель.
	// Её нельзя молча вычистить: эталон с буквой обязан дать ошибку конфига,
	// иначе он превратится в ложные блокирующие замечания по всему пакету.
	for _, field := range []struct {
		label string
		raw   string
		dst   *string
	}{
		{"inn", org.INN, &org.INN},
		{"kpp", org.KPP, &org.KPP},
		{"ogrn", org.OGRN, &org.OGRN},
	} {
		digits, ok := numericRequisite(field.raw)
		if !ok {
			return Org{}, configErr("%s: поле %s = %q — допускаются только цифры и разделители (пробел, дефис)",
				where, field.label, field.raw)
		}
		*field.dst = digits
	}
	for _, field := range []struct {
		label string
		value string
		check func(string) (bool, string)
	}{
		{"inn", org.INN, reqs.ValidateINN},
		{"kpp", org.KPP, reqs.ValidateKPP},
		{"ogrn", org.OGRN, reqs.ValidateOGRN},
	} {
		if field.value == "" {
			continue
		}
		if ok, reason := field.check(field.value); !ok {
			return Org{}, configErr("%s: поле %s = %q — %s", where, field.label, field.value, reason)
		}
	}
	if org.Name == "" && org.Address == "" && org.INN == "" && org.KPP == "" && org.OGRN == "" {
		return Org{}, configErr("%s: не задан ни один реквизит — сверять эталон не с чем", where)
	}
	return org, nil
}

// parseCompositionItem разбирает один пункт требуемого состава и проверяет
// его способ сопоставления. Паттерн имени файла валидируется здесь, а не в
// обработчике: битый glob — опечатка оператора (код выхода 2), а не сбой
// проверки посреди прогона.
func parseCompositionItem(index int, data json.RawMessage) (CompositionItem, error) {
	var item CompositionItem
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&item); err != nil {
		return CompositionItem{}, configErr("пункт состава #%d: структура пункта неверна: %v", index+1, err)
	}

	item.ID = strings.TrimSpace(item.ID)
	if item.ID == "" {
		return CompositionItem{}, configErr("пункт состава #%d: поле id обязательно и не может быть пустым", index+1)
	}
	item.Title = strings.TrimSpace(item.Title)
	if item.Title == "" {
		return CompositionItem{}, configErr("пункт состава #%d (id %q): поле title обязательно и не может быть пустым", index+1, item.ID)
	}

	where := fmt.Sprintf("пункт состава #%d (id %q)", index+1, item.ID)
	switch item.Match.By {
	case matchByFilename:
		if item.Match.Kind != "" {
			return CompositionItem{}, configErr("%s: match.kind = %q не используется при match.by = %q",
				where, item.Match.Kind, matchByFilename)
		}
		if strings.TrimSpace(item.Match.Pattern) == "" {
			return CompositionItem{}, configErr("%s: match.pattern обязателен при match.by = %q", where, matchByFilename)
		}
		// Glob проверяется только когда в паттерне есть «*»: без него паттерн
		// сравнивается буквально (см. matchComposition), и скобки в имени
		// «договор [1].docx» — символ имени файла, а не синтаксис glob.
		if strings.Contains(item.Match.Pattern, "*") && badGlobPattern(item.Match.Pattern) {
			return CompositionItem{}, configErr("%s: match.pattern = %q — неверный glob: незакрытая символьная группа «[»",
				where, item.Match.Pattern)
		}
	case matchByKind:
		if item.Match.Pattern != "" {
			return CompositionItem{}, configErr("%s: match.pattern не используется при match.by = %q", where, matchByKind)
		}
		if !inList(item.Match.Kind, scannerKinds) {
			return CompositionItem{}, configErr("%s: match.kind = %q — допустимы %s",
				where, item.Match.Kind, strings.Join(scannerKinds, ", "))
		}
	case matchByRequisite:
		if item.Match.Pattern != "" {
			return CompositionItem{}, configErr("%s: match.pattern не используется при match.by = %q", where, matchByRequisite)
		}
		if !inList(item.Match.Kind, requisiteKinds) {
			return CompositionItem{}, configErr("%s: match.kind = %q — допустимы %s",
				where, item.Match.Kind, strings.Join(requisiteKinds, ", "))
		}
	case "":
		return CompositionItem{}, configErr("%s: поле match.by обязательно — %s, %s или %s",
			where, matchByFilename, matchByKind, matchByRequisite)
	default:
		return CompositionItem{}, configErr("%s: match.by = %q — допустимы %s, %s или %s",
			where, item.Match.By, matchByFilename, matchByKind, matchByRequisite)
	}
	return item, nil
}

// inList — значение присутствует в наборе допустимых.
func inList(value string, list []string) bool {
	for _, v := range list {
		if value == v {
			return true
		}
	}
	return false
}

// badGlobPattern — glob синтаксически неверен: символьная группа «[» не
// закрыта. Проверять через filepath.Match(pattern, "") нельзя: Match сообщает
// об ошибке только для той части паттерна, до которой дошёл при сопоставлении,
// и незакрытая группа после «*» или после несовпавшего литерала ускользает.
// Правило первого «]» совпадает с Match: сразу после «[» (или «[^») он входит
// в группу как литерал, а не закрывает её.
func badGlobPattern(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] != '[' {
			continue
		}
		i++
		if i < len(pattern) && pattern[i] == '^' {
			i++
		}
		if i < len(pattern) && pattern[i] == ']' {
			i++
		}
		closed := false
		for ; i < len(pattern); i++ {
			if pattern[i] == ']' {
				closed = true
				break
			}
		}
		if !closed {
			return true
		}
	}
	return false
}

// numericRequisite приводит числовой реквизит эталона к цифрам: пробелы и
// дефисы считаются разделителями и убираются, любая другая нецифра (буква,
// точка, знак) делает значение недопустимым. Второе возвращаемое значение —
// признак успеха; при false вызывающий код отвергает эталон.
func numericRequisite(raw string) (string, bool) {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '\u00A0' || r == '\t':
			// разделители разрядов — пропускаем
		default:
			return "", false
		}
	}
	return b.String(), true
}

// digitsOnly оставляет в строке только цифры: нормализация значений карточки
// документа перед сравнением (значения уже извлечены регэкспом \b\d+\b, букв в
// них не бывает, поэтому здесь — в отличие от numericRequisite — достаточно
// просто отбросить нецифры).
func digitsOnly(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
