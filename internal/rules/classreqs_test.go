package rules

import (
	"strings"
	"testing"

	"tenderguy/internal/reqs"
	"tenderguy/internal/trace"
)

// Тесты класса правил «реквизиты» (P2.3): сверка с эталонами из конфига,
// внутренняя консистентность пакета, уровни замечаний и инвариант адресности.
// Карточки документов собираются вручную, чтобы каждый кейс контролировал
// значения, адреса и их отсутствие.

// testRef — трассируемый адрес .docx: файл + абзац.
func testRef(file string, para int) trace.Ref {
	return trace.Ref{File: file, Format: trace.FormatDocx, Para: para}
}

// testItem — значение реквизита в карточке с адресами.
func testItem(kind, value string, refs ...trace.Ref) reqs.Item {
	return reqs.Item{
		Kind: kind, Value: value, Raw: value, Valid: true,
		Hits: 1, Refs: refs, RefTotal: len(refs),
	}
}

// testDoc — документ пакета с карточкой реквизитов.
func testDoc(rel string, items ...reqs.Item) Doc {
	return Doc{
		Path:    "/пакет/" + rel,
		RelPath: rel,
		Format:  trace.FormatDocx,
		Reqs:    &reqs.Card{Path: "/пакет/" + rel, Name: rel, Format: trace.FormatDocx, Items: items},
	}
}

// testOrgs — эталоны: поставщик (полный набор) и заказчик (наименование, ИНН).
// ИНН валидны по контрольным цифрам: 7707083893 и 7736207543.
func testOrgs() []Org {
	return []Org{
		{
			ID: "supplier", Name: "ООО «Ромашка»",
			INN: "7707083893", KPP: "773601001",
			Address: "Москва, ул. Тверская, д. 1",
		},
		{
			ID: "customer", Name: "ПАО «Вектор»",
			INN: "7736207543",
		},
	}
}

// runReqs прогоняет пакет через движок с зарегистрированным классом «reqs».
// Прогон идёт через Runner.Run, поэтому инвариант адресности и severity
// (Finding.Validate) проверяется на каждом замечании автоматически: ошибка
// валидации обернулась бы ошибкой прогона и упавшим тестом.
func runReqs(t *testing.T, orgs []Org, severity Severity, pkg Package) Report {
	t.Helper()

	runner := &Runner{}
	runner.Register(ClassReqs, NewReqsHandler(orgs))
	cfg := Config{
		Version: SupportedVersion,
		Orgs:    orgs,
		Rules:   []Rule{{ID: "reqs-1", Class: ClassReqs, Severity: severity}},
	}
	rep, err := runner.Run(pkg, cfg)
	if err != nil {
		t.Fatalf("Run: неожиданная ошибка: %v", err)
	}
	return rep
}

// findText — замечания, чей текст содержит подстроку.
func findText(rep Report, sub string) []Finding {
	var out []Finding
	for _, f := range rep.Findings {
		if strings.Contains(f.Text, sub) {
			out = append(out, f)
		}
	}
	return out
}

func TestClassReqsConstant(t *testing.T) {
	if ClassReqs != "reqs" {
		t.Errorf("ClassReqs = %q, хотели %q", ClassReqs, "reqs")
	}
}

func TestReqsClassMatchesEtalon(t *testing.T) {
	// Наименование — в другом регистре и с другими кавычками, адрес — с полным
	// словом «улица»: нормализация P2.2 обязана признать их эквивалентными.
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{testDoc("заявка.docx",
			testItem(reqs.KindOrgName, `ооо "ромашка"`, testRef("/пакет/заявка.docx", 1)),
			testItem(reqs.KindINN10, "7707083893", testRef("/пакет/заявка.docx", 2)),
			testItem(reqs.KindKPP, "773601001", testRef("/пакет/заявка.docx", 3)),
			testItem(reqs.KindAddress, "Москва, улица Тверская, д. 1", testRef("/пакет/заявка.docx", 4)),
		)},
	}

	rep := runReqs(t, testOrgs(), SeverityBlocking, pkg)
	if len(rep.Findings) != 0 {
		t.Errorf("совпадающий пакет дал %d замечаний: %+v", len(rep.Findings), rep.Findings)
	}
}

func TestReqsClassEtalonMismatch(t *testing.T) {
	// ИНН документа не совпадает ни с одним эталоном; значение стоит в двух
	// местах — замечание обязано нести оба адреса, значение документа и
	// значение эталона (критерий 2 #58), и блокировать пакет.
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{testDoc("заявка.docx",
			testItem(reqs.KindOrgName, "ООО «Ромашка»", testRef("/пакет/заявка.docx", 1)),
			testItem(reqs.KindINN10, "1234567894",
				testRef("/пакет/заявка.docx", 2), testRef("/пакет/заявка.docx", 9)),
		)},
	}

	rep := runReqs(t, testOrgs(), SeverityWarning, pkg)
	mismatches := findText(rep, "не совпадает с эталоном")
	if len(mismatches) != 1 {
		t.Fatalf("нашли %d замечаний «не совпадает с эталоном»: %+v", len(mismatches), rep.Findings)
	}
	f := mismatches[0]
	if f.Severity != SeverityBlocking {
		t.Errorf("severity = %q, хотели blocking: расхождение с эталоном блокирует независимо от severity правила", f.Severity)
	}
	for _, want := range []string{"ИНН", "1234567894", "7707083893", "ООО «Ромашка»"} {
		if !strings.Contains(f.Text, want) {
			t.Errorf("в тексте нет %q: %s", want, f.Text)
		}
	}
	if len(f.Refs) != 2 {
		t.Errorf("адресов = %d, хотели 2 (все места значения): %+v", len(f.Refs), f.Refs)
	}
	if f.Rule != "reqs-1" || f.Class != ClassReqs {
		t.Errorf("Rule/Class = %q/%q, хотели reqs-1/reqs", f.Rule, f.Class)
	}
}

func TestReqsClassMissingFieldIsWarning(t *testing.T) {
	// В документе нет КПП и адреса, заданных эталоном: warning «реквизит не
	// найден» (критерий 4 #58), а адрес замечания указывает на документ.
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{testDoc("форма.docx",
			testItem(reqs.KindOrgName, "ООО «Ромашка»", testRef("/пакет/форма.docx", 1)),
			testItem(reqs.KindINN10, "7707083893", testRef("/пакет/форма.docx", 2)),
		)},
	}

	rep := runReqs(t, testOrgs(), SeverityBlocking, pkg)
	if rep.Blocking != 0 {
		t.Errorf("блокирующих = %d, хотели 0: отсутствие поля не блокирует", rep.Blocking)
	}
	missing := findText(rep, "реквизит не найден")
	if len(missing) != 2 { // КПП и юридический адрес
		t.Fatalf("нашли %d замечаний «реквизит не найден»: %+v", len(missing), rep.Findings)
	}
	for _, f := range missing {
		if f.Severity != SeverityWarning {
			t.Errorf("severity = %q, хотели warning", f.Severity)
		}
		if len(f.Refs) == 0 || !f.Refs[0].Traced() {
			t.Errorf("у «реквизит не найден» нет трассируемого адреса: %+v", f.Refs)
		}
	}
	if len(findText(rep, "КПП: реквизит не найден")) != 1 {
		t.Errorf("нет замечания про КПП: %+v", rep.Findings)
	}
	if len(findText(rep, "юридический адрес: реквизит не найден")) != 1 {
		t.Errorf("нет замечания про адрес: %+v", rep.Findings)
	}
}

func TestReqsClassInconsistentBetweenDocs(t *testing.T) {
	// Один и тот же реквизит различается между документами (критерий 3 #58):
	// значение первого документа совпадает с эталоном, второго — нет.
	// Замечание консистентности несёт адреса обеих сторон, а его severity
	// берётся из правила (warning), а не из жёсткого blocking сверки с эталоном.
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{
			testDoc("заявка.docx",
				testItem(reqs.KindINN10, "7707083893", testRef("/пакет/заявка.docx", 2))),
			testDoc("договор.docx",
				testItem(reqs.KindINN10, "1234567894", testRef("/пакет/договор.docx", 7))),
		},
	}

	rep := runReqs(t, testOrgs(), SeverityWarning, pkg)
	incons := findText(rep, "различается между документами")
	if len(incons) != 1 {
		t.Fatalf("нашли %d замечаний консистентности: %+v", len(incons), rep.Findings)
	}
	f := incons[0]
	if f.Severity != SeverityWarning {
		t.Errorf("severity = %q, хотели warning из правила конфига", f.Severity)
	}
	for _, want := range []string{"ИНН", "7707083893", "1234567894", "заявка.docx", "договор.docx"} {
		if !strings.Contains(f.Text, want) {
			t.Errorf("в тексте нет %q: %s", want, f.Text)
		}
	}
	// Адреса обеих сторон: и эталонного значения, и расходящегося.
	files := map[string]bool{}
	for _, ref := range f.Refs {
		files[ref.File] = true
	}
	if !files["/пакет/заявка.docx"] || !files["/пакет/договор.docx"] {
		t.Errorf("в адресах нет обеих сторон: %+v", f.Refs)
	}
	// Расхождение второго документа с эталоном — отдельное блокирующее.
	if len(findText(rep, "не совпадает с эталоном")) != 1 {
		t.Errorf("нет блокирующего замечания сверки с эталоном: %+v", rep.Findings)
	}
}

func TestReqsClassBothValuesInEtalons(t *testing.T) {
	// Пакет несёт реквизиты обеих законных сторон (поставщик и заказчик):
	// различие между документами объяснено эталонами и замечанием не является.
	// Эталоны здесь без КПП и адреса, чтобы в пакет не приходили предупреждения
	// «реквизит не найден» по полям, которые документы сторон не обязаны нести.
	orgs := []Org{
		{ID: "supplier", Name: "ООО «Ромашка»", INN: "7707083893"},
		{ID: "customer", Name: "ПАО «Вектор»", INN: "7736207543"},
	}
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{
			testDoc("заявка.docx",
				testItem(reqs.KindOrgName, "ООО «Ромашка»", testRef("/пакет/заявка.docx", 1)),
				testItem(reqs.KindINN10, "7707083893", testRef("/пакет/заявка.docx", 2))),
			testDoc("договор.docx",
				testItem(reqs.KindOrgName, "ПАО «Вектор»", testRef("/пакет/договор.docx", 1)),
				testItem(reqs.KindINN10, "7736207543", testRef("/пакет/договор.docx", 2))),
		},
	}

	rep := runReqs(t, orgs, SeverityBlocking, pkg)
	if len(rep.Findings) != 0 {
		t.Errorf("пакет двух законных сторон дал %d замечаний: %+v", len(rep.Findings), rep.Findings)
	}
}

func TestReqsClassWorksWithoutEtalons(t *testing.T) {
	// Раздел orgs пуст: сверки с эталоном нет, но внутренняя консистентность
	// обязана работать — разные ИНН в двух документах дают замечание уровня
	// правила, и только одно (сверка с эталоном молчит).
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{
			testDoc("заявка.docx",
				testItem(reqs.KindINN10, "7707083893", testRef("/пакет/заявка.docx", 2))),
			testDoc("договор.docx",
				testItem(reqs.KindINN10, "1234567894", testRef("/пакет/договор.docx", 7))),
		},
	}

	rep := runReqs(t, nil, SeverityWarning, pkg)
	if len(findText(rep, "не совпадает с эталоном")) != 0 {
		t.Errorf("без эталонов не должно быть сверки с эталоном: %+v", rep.Findings)
	}
	incons := findText(rep, "различается между документами")
	if len(incons) != 1 {
		t.Fatalf("нашли %d замечаний консистентности: %+v", len(incons), rep.Findings)
	}
	if incons[0].Severity != SeverityWarning {
		t.Errorf("severity = %q, хотели warning из правила", incons[0].Severity)
	}
}

func TestReqsClassFallbackToFragmentRef(t *testing.T) {
	// У значения нет адресов (дыра парсера), но фрагменты документа адресованы:
	// замечание берёт запасной адрес документа и остаётся трассируемым.
	doc := testDoc("форма.docx", testItem(reqs.KindINN10, "1234567894"))
	doc.Fragments = []trace.Fragment{{
		Ref:  testRef("/пакет/форма.docx", 1),
		Text: "ИНН: 1234567894",
	}}
	pkg := Package{Root: "/пакет", Docs: []Doc{doc}}

	rep := runReqs(t, testOrgs(), SeverityBlocking, pkg)
	mismatches := findText(rep, "не совпадает с эталоном")
	if len(mismatches) != 1 {
		t.Fatalf("нашли %d замечаний: %+v", len(mismatches), rep.Findings)
	}
	if len(mismatches[0].Refs) != 1 || !mismatches[0].Refs[0].Traced() {
		t.Errorf("запасной адрес не подставился: %+v", mismatches[0].Refs)
	}
}

func TestReqsClassIPRequisites(t *testing.T) {
	// Этalon — индивидуальный предприниматель: ИНН из 12 цифр (вид inn12) и
	// ОГРНИП из 15 цифр (вид ogrnip). Проверка выбора вида по длине эталона и
	// сверки по цифровому значению для этих веток (orgINNKind/orgOGRNKind).
	orgs := []Org{{ID: "ip", Name: "ИП Иванов", INN: "771456789041", OGRN: "304770000000008"}}

	t.Run("совпадение — нет замечаний", func(t *testing.T) {
		pkg := Package{Root: "/пакет", Docs: []Doc{testDoc("заявка.docx",
			testItem(reqs.KindINN12, "771456789041", testRef("/пакет/заявка.docx", 1)),
			testItem(reqs.KindOGRNIP, "304770000000008", testRef("/пакет/заявка.docx", 2)),
		)}}
		rep := runReqs(t, orgs, SeverityBlocking, pkg)
		if n := len(findText(rep, "не совпадает с эталоном")); n != 0 {
			t.Errorf("совпадающий ИП-пакет дал %d замечаний сверки: %+v", n, rep.Findings)
		}
	})

	t.Run("чужой ИНН ИП — блокирующее с меткой ИНН", func(t *testing.T) {
		pkg := Package{Root: "/пакет", Docs: []Doc{testDoc("заявка.docx",
			testItem(reqs.KindINN12, "771456789042", testRef("/пакет/заявка.docx", 1)),
			testItem(reqs.KindOGRNIP, "304770000000008", testRef("/пакет/заявка.docx", 2)),
		)}}
		rep := runReqs(t, orgs, SeverityBlocking, pkg)
		inns := findText(rep, "ИНН в документе")
		if len(inns) != 1 {
			t.Fatalf("нашли %d замечаний ИНН: %+v", len(inns), rep.Findings)
		}
		if inns[0].Severity != SeverityBlocking {
			t.Errorf("severity = %q, хотели blocking", inns[0].Severity)
		}
		for _, want := range []string{"771456789042", "771456789041"} {
			if !strings.Contains(inns[0].Text, want) {
				t.Errorf("в тексте нет %q: %s", want, inns[0].Text)
			}
		}
	})

	t.Run("чужой ОГРНИП — блокирующее с меткой ОГРНИП", func(t *testing.T) {
		pkg := Package{Root: "/пакет", Docs: []Doc{testDoc("заявка.docx",
			testItem(reqs.KindINN12, "771456789041", testRef("/пакет/заявка.docx", 1)),
			testItem(reqs.KindOGRNIP, "304770000000009", testRef("/пакет/заявка.docx", 2)),
		)}}
		rep := runReqs(t, orgs, SeverityBlocking, pkg)
		if len(findText(rep, "ОГРНИП в документе")) != 1 {
			t.Errorf("нет замечания ОГРНИП: %+v", rep.Findings)
		}
	})
}

func TestReqsClassUnmatchedValueAlongsideMatch(t *testing.T) {
	// Документ несёт два значения одного вида: корректный ИНН поставщика
	// (эталон) в шапке и «чужой» ИНН субподрядчика в теле. Сверка идёт по
	// значению, а не по документу: чужой ИНН обязан дать блокирующее замечание
	// и НЕ прятаться за соседним корректным (фикс блокирующего замечания ревью).
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{testDoc("договор.docx",
			testItem(reqs.KindOrgName, "ООО «Ромашка»", testRef("/пакет/договор.docx", 1)),
			testItem(reqs.KindINN10, "7707083893", testRef("/пакет/договор.docx", 2)),
			testItem(reqs.KindINN10, "1234567894", testRef("/пакет/договор.docx", 9)),
		)},
	}

	rep := runReqs(t, testOrgs(), SeverityBlocking, pkg)
	mismatches := findText(rep, "ИНН в документе")
	if len(mismatches) != 1 {
		t.Fatalf("нашли %d замечаний ИНН: %+v", len(mismatches), rep.Findings)
	}
	f := mismatches[0]
	if f.Severity != SeverityBlocking {
		t.Errorf("severity = %q, хотели blocking", f.Severity)
	}
	// В тексте — только чужое значение, корректный ИНН поставщика не фигурирует.
	if !strings.Contains(f.Text, "1234567894") {
		t.Errorf("в тексте нет чужого ИНН 1234567894: %s", f.Text)
	}
	if strings.Contains(f.Text, "7707083893,") {
		t.Errorf("корректный ИНН не должен попадать в расхождение: %s", f.Text)
	}
	// Адрес указывает на место чужого значения (абз. 9), а не корректного.
	found := false
	for _, ref := range f.Refs {
		if ref.Para == 9 {
			found = true
		}
	}
	if !found {
		t.Errorf("в адресах нет места чужого значения (абз. 9): %+v", f.Refs)
	}
}

func TestReqsClassEveryFindingValidate(t *testing.T) {
	// Инвариант адресности и severity (критерий 5 #58): каждое замечание класса
	// проходит Finding.Validate — тот же, которым его проверяет Runner.Run.
	pkg := Package{
		Root: "/пакет",
		Docs: []Doc{
			testDoc("заявка.docx",
				testItem(reqs.KindOrgName, "ООО «Ромашка»", testRef("/пакет/заявка.docx", 1)),
				testItem(reqs.KindINN10, "7707083893", testRef("/пакет/заявка.docx", 2))),
			testDoc("договор.docx",
				testItem(reqs.KindOrgName, "ЗАО «Лютик»", testRef("/пакет/договор.docx", 1)),
				testItem(reqs.KindINN10, "1234567894", testRef("/пакет/договор.docx", 2))),
		},
	}

	rep := runReqs(t, testOrgs(), SeverityBlocking, pkg)
	if len(rep.Findings) == 0 {
		t.Fatal("пакет с чужими реквизитами обязан дать замечания")
	}
	for _, f := range rep.Findings {
		if err := f.Validate(); err != nil {
			t.Errorf("Validate(%q): %v", f.Text, err)
		}
	}
}
