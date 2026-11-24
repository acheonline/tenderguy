package rules

import (
	"reflect"
	"testing"

	"tenderguy/internal/trace"
)

// Тесты подготовки отчёта (P2.6): группировка замечаний по классам и
// критичности и сводка-счётчики. Обе функции обязаны быть детерминированными:
// порядок классов — алфавитный, блокирующие раньше остальных, порядок входа
// внутри группы сохраняется.

func reportFinding(class Class, sev Severity, rule string) Finding {
	return Finding{Rule: rule, Class: class, Severity: sev, Text: rule + " текст", Refs: []trace.Ref{testTracedRef()}}
}

func TestGroupFindings(t *testing.T) {
	tests := []struct {
		name string
		in   []Finding
		want []ClassGroup
	}{
		{
			name: "пустой вход — nil",
			in:   nil,
			want: nil,
		},
		{
			name: "классы по алфавиту, блокирующие раньше предупреждений, порядок входа сохранён",
			in: []Finding{
				reportFinding("reqs", SeverityWarning, "w1"),
				reportFinding("composition", SeverityWarning, "c-w"),
				reportFinding("reqs", SeverityBlocking, "b2"),
				reportFinding("reqs", SeverityBlocking, "b1"),
			},
			want: []ClassGroup{
				{Class: "composition", Findings: []Finding{reportFinding("composition", SeverityWarning, "c-w")}},
				{Class: "reqs", Findings: []Finding{
					reportFinding("reqs", SeverityBlocking, "b2"),
					reportFinding("reqs", SeverityBlocking, "b1"),
					reportFinding("reqs", SeverityWarning, "w1"),
				}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GroupFindings(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("GroupFindings() = %+v, хотели %+v", got, tc.want)
			}
		})
	}
}

// TestGroupFindingsDeterministic — одинаковый вход даёт одинаковый выход при
// повторных вызовах (итерация по карте внутри не должна протекать в порядок).
func TestGroupFindingsDeterministic(t *testing.T) {
	in := []Finding{
		reportFinding("zeta", SeverityWarning, "z"),
		reportFinding("alpha", SeverityBlocking, "a"),
		reportFinding("middle", SeverityWarning, "m"),
	}
	first := GroupFindings(in)
	for i := 0; i < 20; i++ {
		if got := GroupFindings(in); !reflect.DeepEqual(got, first) {
			t.Fatalf("прогон %d: GroupFindings() = %+v, первый прогон %+v", i+1, got, first)
		}
	}
}

func TestSummarizeClasses(t *testing.T) {
	tests := []struct {
		name string
		in   []Finding
		want []ClassSummary
	}{
		{
			name: "пустой вход — nil",
			in:   []Finding{},
			want: nil,
		},
		{
			name: "счётчики по классам в алфавитном порядке",
			in: []Finding{
				reportFinding("reqs", SeverityBlocking, "b1"),
				reportFinding("composition", SeverityWarning, "c-w"),
				reportFinding("reqs", SeverityWarning, "w1"),
				reportFinding("reqs", SeverityBlocking, "b2"),
			},
			want: []ClassSummary{
				{Class: "composition", Total: 1, Blocking: 0, Warnings: 1},
				{Class: "reqs", Total: 3, Blocking: 2, Warnings: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SummarizeClasses(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("SummarizeClasses() = %+v, хотели %+v", got, tc.want)
			}
		})
	}
}

// TestGroupFindingsDoesNotMutateInput — контракт GroupFindings: входной срез
// не изменяется (сортировка внутри группы работает на копии, вызывающий код
// продолжает видеть Findings в порядке конфига).
func TestGroupFindingsDoesNotMutateInput(t *testing.T) {
	in := []Finding{
		reportFinding("reqs", SeverityWarning, "w1"),
		reportFinding("composition", SeverityBlocking, "c-b"),
		reportFinding("reqs", SeverityBlocking, "b1"),
	}
	before := make([]Finding, len(in))
	copy(before, in)

	if got := GroupFindings(in); len(got) != 2 {
		t.Fatalf("GroupFindings() вернула %d групп, хотели 2", len(got))
	}
	if !reflect.DeepEqual(in, before) {
		t.Errorf("GroupFindings изменила входной срез:\nстало:  %+v\nбыло: %+v", in, before)
	}
}

// TestRunSkippedClasses — зарегистрированный класс без правил в конфиге
// попадает в Report.Skipped с причиной, а класс с правилами — нет.
func TestRunSkippedClasses(t *testing.T) {
	r := &Runner{}
	r.Register("test.used", func(_ Rule, _ Package) ([]Finding, error) { return nil, nil })
	r.Register("test.unused", func(_ Rule, _ Package) ([]Finding, error) { return nil, nil })

	cfg := Config{Version: SupportedVersion, Rules: []Rule{
		{ID: "r1", Class: "test.used", Severity: SeverityWarning},
	}}
	rep, err := r.Run(Package{Root: "/пакет"}, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []Skipped{{Class: "test.unused", Reason: "нет правил класса в конфиге"}}
	if !reflect.DeepEqual(rep.Skipped, want) {
		t.Errorf("Skipped = %+v, хотели %+v", rep.Skipped, want)
	}
}
