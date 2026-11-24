package rules

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"tenderguy/internal/trace"
)

// Тесты Runner (P2.1): поиск обработчика по классу, счётчики и порядок
// замечаний, ошибки прогона и инвариант адресности — замечание без
// trace.Ref с Traced()==true обрывает прогон, как фрагмент без адреса в P1.6.

// testDummy — класс-заглушка: движок не знает смысла проверок, поэтому тесты
// регистрируют обработчики сами.
const testDummy Class = "test.dummy"

func testTracedRef() trace.Ref {
	return trace.Ref{File: "пакет/заявка.docx", Format: trace.FormatDocx, Para: 3}
}

func testFinding(ruleID string, sev Severity, text string, refs ...trace.Ref) Finding {
	return Finding{Rule: ruleID, Class: testDummy, Severity: sev, Text: text, Refs: refs}
}

func TestRunnerRun(t *testing.T) {
	tests := []struct {
		name     string
		handlers map[Class]Handler
		cfg      Config
		pkg      Package
		want     Report
		wantErr  []string
		// errIsConfig — ошибка обязана (true) или не должна (false) нести ErrConfig.
		errIsConfig bool
	}{
		{
			name: "правил нет — пустой отчёт, Findings и Failed не nil",
			cfg:  Config{Version: SupportedVersion},
			pkg:  Package{Root: "/пакет"},
			want: Report{InputDir: "/пакет", Findings: []Finding{}, Failed: []Failed{}, Skipped: []Skipped{}},
		},
		{
			name: "замечания считаются, порядок правил конфига сохраняется",
			handlers: map[Class]Handler{
				testDummy: func(r Rule, _ Package) ([]Finding, error) {
					switch r.ID {
					case "b":
						return []Finding{
							testFinding("b", SeverityBlocking, "b-блокирующее", testTracedRef()),
							testFinding("b", SeverityWarning, "b-предупреждение", testTracedRef()),
						}, nil
					case "a":
						return []Finding{testFinding("a", SeverityBlocking, "a-блокирующее", testTracedRef())}, nil
					}
					return nil, nil
				},
			},
			// Порядок правил в конфиге намеренно «b, a»: отчёт читается по
			// списку, и сортировка задним числом сломала бы адресность чтения.
			cfg: Config{Version: SupportedVersion, Rules: []Rule{
				{ID: "b", Class: testDummy, Severity: SeverityBlocking},
				{ID: "a", Class: testDummy, Severity: SeverityWarning},
			}},
			pkg: Package{Root: "/пакет"},
			want: Report{
				InputDir:   "/пакет",
				RulesTotal: 2,
				Blocking:   2,
				Warnings:   1,
				Findings: []Finding{
					testFinding("b", SeverityBlocking, "b-блокирующее", testTracedRef()),
					testFinding("b", SeverityWarning, "b-предупреждение", testTracedRef()),
					testFinding("a", SeverityBlocking, "a-блокирующее", testTracedRef()),
				},
				Failed:  []Failed{},
				Skipped: []Skipped{},
			},
		},
		{
			name:     "неразобранные документы пакета попадают в отчёт",
			handlers: map[Class]Handler{testDummy: func(_ Rule, _ Package) ([]Finding, error) { return nil, nil }},
			cfg: Config{Version: SupportedVersion, Rules: []Rule{
				{ID: "r1", Class: testDummy, Severity: SeverityWarning},
			}},
			pkg: Package{
				Root:   "/пакет",
				Failed: []Failed{{Path: "/пакет/скан.pdf", Error: "pdf не читается"}},
			},
			want: Report{
				InputDir:   "/пакет",
				RulesTotal: 1,
				Findings:   []Finding{},
				Failed:     []Failed{{Path: "/пакет/скан.pdf", Error: "pdf не читается"}},
				Skipped:    []Skipped{},
			},
		},
		{
			name: "ошибка обработчика обрывает прогон",
			handlers: map[Class]Handler{testDummy: func(_ Rule, _ Package) ([]Finding, error) {
				return nil, errors.New("сбой проверки")
			}},
			cfg: Config{Version: SupportedVersion, Rules: []Rule{
				{ID: "x", Class: testDummy, Severity: SeverityBlocking},
			}},
			pkg:         Package{Root: "/пакет"},
			wantErr:     []string{`правило #1 (id "x", класс "test.dummy")`, "сбой проверки"},
			errIsConfig: false,
		},
		{
			name:     "неизвестный класс — ошибка конфига",
			handlers: map[Class]Handler{testDummy: func(_ Rule, _ Package) ([]Finding, error) { return nil, nil }},
			cfg: Config{Version: SupportedVersion, Rules: []Rule{
				{ID: "r1", Class: testDummy, Severity: SeverityWarning},
				{ID: "r2", Class: "test.missing", Severity: SeverityWarning},
			}},
			pkg:         Package{Root: "/пакет"},
			wantErr:     []string{`правило #2 (id "r2")`, `"test.missing"`, "неизвестно движку", "test.dummy"},
			errIsConfig: true,
		},
		{
			name: "неизвестный класс при пустом движке — сообщение объясняет причину",
			cfg: Config{Version: SupportedVersion, Rules: []Rule{
				{ID: "r1", Class: "test.missing", Severity: SeverityWarning},
			}},
			pkg:         Package{Root: "/пакет"},
			wantErr:     []string{"неизвестно движку", "зарегистрированных классов нет"},
			errIsConfig: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{}
			for class, handler := range tc.handlers {
				r.Register(class, handler)
			}

			got, err := r.Run(tc.pkg, tc.cfg)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Run: неожиданная ошибка: %v", err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("отчёт = %+v, хотели %+v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("Run: хотели ошибку, получили отчёт %+v", got)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в ошибке нет %q: %v", want, err)
				}
			}
			if tc.errIsConfig && !errors.Is(err, ErrConfig) {
				t.Errorf("ошибка конфига обязана нести ErrConfig: %v", err)
			}
			if !tc.errIsConfig && errors.Is(err, ErrConfig) {
				t.Errorf("внутренняя ошибка прогона не должна нести ErrConfig: %v", err)
			}
		})
	}
}

func TestRunnerClasses(t *testing.T) {
	tests := []struct {
		name     string
		register []Class
		want     []Class
	}{
		{
			name: "пустой движок — пустой список, не nil",
			want: []Class{},
		},
		{
			name:     "классы возвращаются в алфавитном порядке",
			register: []Class{"test.charlie", "test.alpha", "test.bravo"},
			want:     []Class{"test.alpha", "test.bravo", "test.charlie"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{}
			for _, class := range tc.register {
				r.Register(class, func(_ Rule, _ Package) ([]Finding, error) { return nil, nil })
			}
			got := r.Classes()
			if got == nil {
				t.Fatal("Classes() = nil, хотели пустой срез")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Classes() = %v, хотели %v", got, tc.want)
			}
		})
	}
}

func TestRunnerRegisterOverwrites(t *testing.T) {
	r := &Runner{}
	r.Register(testDummy, func(_ Rule, _ Package) ([]Finding, error) {
		return []Finding{testFinding("r1", SeverityWarning, "старый обработчик", testTracedRef())}, nil
	})
	r.Register(testDummy, func(_ Rule, _ Package) ([]Finding, error) {
		return []Finding{testFinding("r1", SeverityWarning, "новый обработчик", testTracedRef())}, nil
	})

	if got := r.Classes(); !reflect.DeepEqual(got, []Class{testDummy}) {
		t.Fatalf("после повторной регистрации классов %v, хотели один %q", got, testDummy)
	}
	rep, err := r.Run(Package{Root: "/пакет"}, Config{Version: SupportedVersion, Rules: []Rule{
		{ID: "r1", Class: testDummy, Severity: SeverityWarning},
	}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Findings) != 1 || rep.Findings[0].Text != "новый обработчик" {
		t.Errorf("замечания = %+v, хотели одно от нового обработчика", rep.Findings)
	}
}

// TestEveryFindingHasAddress — инвариант адресности P2.1 по образцу
// TestEveryFragmentHasAddress (P1.6): прогон обязан отклонить замечание,
// у которого нет ни одного trace.Ref с Traced()==true, иначе специалист
// не сможет открыть место замечания в документе.
func TestEveryFindingHasAddress(t *testing.T) {
	fileOnly := trace.Ref{File: "пакет/заявка.docx", Format: trace.FormatDocx}
	placeOnly := trace.Ref{Format: trace.FormatXlsx, Sheet: "ТЗ", Cell: "B7"}
	traced := testTracedRef()

	tests := []struct {
		name    string
		refs    []trace.Ref
		wantErr []string
	}{
		{
			name:    "замечание без адресов отклоняется",
			refs:    nil,
			wantErr: []string{`правило #1 (id "r1"`, "нет ни одного адреса"},
		},
		{
			name:    "адреса без Traced()==true отклоняются",
			refs:    []trace.Ref{fileOnly, placeOnly},
			wantErr: []string{"нет ни одного адреса"},
		},
		{
			name: "один трассируемый адрес среди нетрассируемых — прогон проходит",
			refs: []trace.Ref{fileOnly, traced},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{}
			r.Register(testDummy, func(_ Rule, _ Package) ([]Finding, error) {
				return []Finding{testFinding("r1", SeverityBlocking, "реквизит не найден", tc.refs...)}, nil
			})

			rep, err := r.Run(Package{Root: "/пакет"}, Config{Version: SupportedVersion, Rules: []Rule{
				{ID: "r1", Class: testDummy, Severity: SeverityBlocking},
			}})
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("Run: неожиданная ошибка: %v", err)
				}
				if len(rep.Findings) != 1 {
					t.Fatalf("замечаний %d, хотели 1", len(rep.Findings))
				}
				if rep.Blocking != 1 {
					t.Errorf("Blocking = %d, хотели 1", rep.Blocking)
				}
				return
			}
			if err == nil {
				t.Fatalf("Run: хотели ошибку адресности, получили отчёт %+v", rep)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в ошибке нет %q: %v", want, err)
				}
			}
			// Отсутствие адреса — программная ошибка класса правил, а не
			// свойства конфига: ErrConfig здесь менял бы код выхода CLI.
			if errors.Is(err, ErrConfig) {
				t.Errorf("ошибка адресности не должна нести ErrConfig: %v", err)
			}
		})
	}
}
