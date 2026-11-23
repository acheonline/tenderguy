package rules

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"tenderguy/internal/reqs"
	"tenderguy/internal/trace"
)

// Тесты движка детерминированных правил (P2.1): LoadConfig разбирает и
// проверяет JSON-конфиг, Finding.Validate держит инвариант адресности одного
// замечания. Фикстуры синтетические — JSON-строки во временном каталоге.

// writeRulesConfig пишет содержимое конфига в файл внутри t.TempDir и
// возвращает путь к нему.
func writeRulesConfig(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("запись фикстуры правил: %v", err)
	}
	return path
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name string
		// content — содержимое файла правил; игнорируется при missingFile.
		content string
		// missingFile — вместо фикстуры подаём несуществующий путь: ошибка
		// чтения обязана отличаться от ошибки содержимого (коды выхода 3 и 2).
		missingFile bool
		want        Config
		wantErr     []string
		// errKind — какой маркер обязана нести ошибка: "config" (ErrConfig),
		// "path" (НЕ ErrConfig) или "" (ошибки нет).
		errKind string
	}{
		{
			name:    "битый JSON — ошибка содержимого",
			content: `{"version": 1,`,
			wantErr: []string{"битый JSON"},
			errKind: "config",
		},
		{
			name:    "неизвестное поле верхнего уровня",
			content: `{"version": 1, "unknown": true}`,
			wantErr: []string{"структура конфига неверна", "unknown"},
			errKind: "config",
		},
		{
			name:    "rules не список",
			content: `{"version": 1, "rules": {}}`,
			wantErr: []string{"структура конфига неверна"},
			errKind: "config",
		},
		{
			name:    "version отсутствует — сообщение называет поддерживаемую версию",
			content: `{"rules": []}`,
			wantErr: []string{"version не задано", "версию 1"},
			errKind: "config",
		},
		{
			name:    "version = 0 приравнивается к отсутствующей",
			content: `{"version": 0}`,
			wantErr: []string{"version не задано", "версию 1"},
			errKind: "config",
		},
		{
			name:    "version = 2 — сообщение называет обе версии",
			content: `{"version": 2}`,
			wantErr: []string{"version = 2", "только версия 1"},
			errKind: "config",
		},
		{
			name:    "неизвестное поле правила",
			content: `{"version": 1, "rules": [{"id": "r1", "class": "c", "severity": "warning", "extra": 1}]}`,
			wantErr: []string{"правило #1", "структура правила неверна", "extra"},
			errKind: "config",
		},
		{
			name:    "пустой id",
			content: `{"version": 1, "rules": [{"id": "  ", "class": "c", "severity": "warning"}]}`,
			wantErr: []string{"правило #1", "id обязательно"},
			errKind: "config",
		},
		{
			name:    "id отсутствует",
			content: `{"version": 1, "rules": [{"class": "c", "severity": "warning"}]}`,
			wantErr: []string{"правило #1", "id обязательно"},
			errKind: "config",
		},
		{
			name:    "пустой class",
			content: `{"version": 1, "rules": [{"id": "r1", "class": "", "severity": "warning"}]}`,
			wantErr: []string{`id "r1"`, "class обязательно"},
			errKind: "config",
		},
		{
			name:    "severity отсутствует — сообщение называет допустимые значения",
			content: `{"version": 1, "rules": [{"id": "r1", "class": "c"}]}`,
			wantErr: []string{"severity обязательно", "blocking", "warning"},
			errKind: "config",
		},
		{
			name:    "severity = fatal — сообщение называет поле, значение и шкалу",
			content: `{"version": 1, "rules": [{"id": "r1", "class": "c", "severity": "fatal"}]}`,
			wantErr: []string{"поле severity", `"fatal"`, "допустимы blocking или warning"},
			errKind: "config",
		},
		{
			name: "дубликат id — сообщение называет оба номера правила",
			content: `{"version": 1, "rules": [
				{"id": "dup", "class": "c", "severity": "warning"},
				{"id": "ok", "class": "c", "severity": "warning"},
				{"id": "dup", "class": "c", "severity": "blocking"}
			]}`,
			wantErr: []string{"правила #1 и #3", `одинаковый id "dup"`},
			errKind: "config",
		},
		{
			name:    "валидный конфиг: params сохраняются сырым JSON",
			content: `{"version": 1, "rules": [{"id": "r1", "class": "test.class", "severity": "blocking", "params": {"threshold": 5, "list": ["a","b"]}}]}`,
			want: Config{
				Version: SupportedVersion,
				Rules: []Rule{{
					ID:       "r1",
					Class:    "test.class",
					Severity: SeverityBlocking,
					Params: map[string]json.RawMessage{
						"threshold": json.RawMessage(`5`),
						"list":      json.RawMessage(`["a","b"]`),
					},
				}},
			},
		},
		{
			name: "валидный эталон: пробелы в ИНН убираются, поля сохраняются",
			content: `{"version": 1, "orgs": [
				{"id": "supplier", "name": "ООО «Ромашка»", "inn": "7707 0838 93", "kpp": "773601001",
				 "ogrn": "1027700132190", "address": "Москва, ул. Тверская, д. 1"}
			]}`,
			want: Config{
				Version: SupportedVersion,
				Orgs: []Org{{
					ID: "supplier", Name: "ООО «Ромашка»",
					INN: "7707083893", KPP: "773601001", OGRN: "1027700132190",
					Address: "Москва, ул. Тверская, д. 1",
				}},
			},
		},
		{
			name:    "эталон без id — ошибка называет номер эталона",
			content: `{"version": 1, "orgs": [{"name": "ООО «Ромашка»"}]}`,
			wantErr: []string{"эталон #1", "id обязательно"},
			errKind: "config",
		},
		{
			name:    "дубликат id эталонов — ошибка называет оба номера",
			content: `{"version": 1, "orgs": [{"id": "s", "inn": "7707083893"}, {"id": "s", "inn": "7736207543"}]}`,
			wantErr: []string{"эталоны #1 и #2", `одинаковый id "s"`},
			errKind: "config",
		},
		{
			name:    "эталон с неверной контрольной цифрой ИНН",
			content: `{"version": 1, "orgs": [{"id": "s", "inn": "7707083894"}]}`,
			wantErr: []string{`эталон #1 (id "s")`, "поле inn", "контрольная цифра не сходится"},
			errKind: "config",
		},
		{
			name:    "эталон с буквой в ИНН — не молчаливая правка, а ошибка",
			content: `{"version": 1, "orgs": [{"id": "s", "inn": "77070838a93"}]}`,
			wantErr: []string{`эталон #1 (id "s")`, "поле inn", "только цифры и разделители"},
			errKind: "config",
		},
		{
			name:    "эталон с неверным ОГРН",
			content: `{"version": 1, "orgs": [{"id": "s", "ogrn": "1027700132191"}]}`,
			wantErr: []string{`эталон #1 (id "s")`, "поле ogrn", "контрольная цифра не сходится"},
			errKind: "config",
		},
		{
			name:    "эталон без единого реквизита",
			content: `{"version": 1, "orgs": [{"id": "s"}]}`,
			wantErr: []string{`эталон #1 (id "s")`, "не задан ни один реквизит"},
			errKind: "config",
		},
		{
			name:    "неизвестное поле эталона",
			content: `{"version": 1, "orgs": [{"id": "s", "inn": "7707083893", "bik": "044525225"}]}`,
			wantErr: []string{"эталон #1", "структура эталона неверна", "bik"},
			errKind: "config",
		},
		{
			name:    "пустой список правил — не ошибка",
			content: `{"version": 1, "rules": []}`,
			want:    Config{Version: SupportedVersion},
		},
		{
			name:    "ключ rules отсутствует — не ошибка",
			content: `{"version": 1}`,
			want:    Config{Version: SupportedVersion},
		},
		{
			name:        "несуществующий файл — ошибка чтения без ErrConfig",
			missingFile: true,
			wantErr:     []string{"не удалось прочитать файл правил"},
			errKind:     "path",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "нет-такого-файла.json")
			if !tc.missingFile {
				path = writeRulesConfig(t, tc.content)
			}

			got, err := LoadConfig(path)
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("LoadConfig(%s): неожиданная ошибка: %v", path, err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("конфиг = %+v, хотели %+v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("LoadConfig(%s): хотели ошибку, получили конфиг %+v", path, got)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в ошибке нет %q: %v", want, err)
				}
			}
			switch tc.errKind {
			case "config":
				if !errors.Is(err, ErrConfig) {
					t.Errorf("ошибка содержимого обязана нести ErrConfig: %v", err)
				}
			case "path":
				if errors.Is(err, ErrConfig) {
					t.Errorf("ошибка чтения файла не должна нести ErrConfig: %v", err)
				}
				if !errors.Is(err, os.ErrNotExist) {
					t.Errorf("ошибка чтения должна нести os.ErrNotExist: %v", err)
				}
			}
		})
	}
}

func TestLoadConfigComposition(t *testing.T) {
	// Раздел composition (P2.4): пункты требуемого состава, их способы
	// сопоставления и ошибки, которые обязан ловить LoadConfig (код выхода 2).
	tests := []struct {
		name    string
		content string
		want    Config
		wantErr []string
	}{
		{
			name: "валидные пункты всех трёх способов сопоставления",
			content: `{"version": 1, "composition": [
				{"id": "form-2", "title": "Заявка по форме 2", "required": true,
				 "match": {"by": "filename", "pattern": "заявка*.docx"}},
				{"id": "price", "title": "Спецификация цены", "required": false,
				 "match": {"by": "kind", "kind": "xlsx"}},
				{"id": "card", "title": "Карточка с реквизитами", "required": true,
				 "match": {"by": "requisite", "kind": "inn10"}}
			]}`,
			want: Config{
				Version: SupportedVersion,
				Composition: []CompositionItem{
					{ID: "form-2", Title: "Заявка по форме 2", Required: true,
						Match: CompositionMatch{By: matchByFilename, Pattern: "заявка*.docx"}},
					{ID: "price", Title: "Спецификация цены",
						Match: CompositionMatch{By: matchByKind, Kind: "xlsx"}},
					{ID: "card", Title: "Карточка с реквизитами", Required: true,
						Match: CompositionMatch{By: matchByRequisite, Kind: reqs.KindINN10}},
				},
			},
		},
		{
			name:    "пункт без id",
			content: `{"version": 1, "composition": [{"title": "Заявка", "match": {"by": "kind", "kind": "docx"}}]}`,
			wantErr: []string{"пункт состава #1", "id обязательно"},
		},
		{
			name:    "пункт без title",
			content: `{"version": 1, "composition": [{"id": "c1", "match": {"by": "kind", "kind": "docx"}}]}`,
			wantErr: []string{`пункт состава #1 (id "c1")`, "title обязательно"},
		},
		{
			name:    "пункт без match",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка"}]}`,
			wantErr: []string{`пункт состава #1 (id "c1")`, "match.by обязательно", "filename", "kind", "requisite"},
		},
		{
			name:    "неизвестный способ сопоставления",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "match": {"by": "magic"}}]}`,
			wantErr: []string{`match.by = "magic"`, "допустимы"},
		},
		{
			name:    "filename без pattern",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "match": {"by": "filename"}}]}`,
			wantErr: []string{"match.pattern обязателен"},
		},
		{
			name:    "filename с битым glob",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "match": {"by": "filename", "pattern": "заявка*[.docx"}}]}`,
			wantErr: []string{"неверный glob"},
		},
		{
			name:    "filename с лишним kind",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "match": {"by": "filename", "pattern": "з*.docx", "kind": "docx"}}]}`,
			wantErr: []string{"match.kind", "не используется при match.by"},
		},
		{
			name:    "kind с неизвестным типом документа",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "match": {"by": "kind", "kind": "txt"}}]}`,
			wantErr: []string{`match.kind = "txt"`, "допустимы", "docx", "office_legacy"},
		},
		{
			name:    "kind с лишним pattern",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "match": {"by": "kind", "kind": "docx", "pattern": "з*.docx"}}]}`,
			wantErr: []string{"match.pattern не используется"},
		},
		{
			name:    "requisite с видом вне набора reqs",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "match": {"by": "requisite", "kind": "date"}}]}`,
			wantErr: []string{`match.kind = "date"`, "допустимы", "org_name"},
		},
		{
			name: "дубликат id пунктов",
			content: `{"version": 1, "composition": [
				{"id": "c1", "title": "Заявка", "match": {"by": "kind", "kind": "docx"}},
				{"id": "c1", "title": "Заявка копия", "match": {"by": "kind", "kind": "pdf"}}
			]}`,
			wantErr: []string{"пункты состава #1 и #2", `одинаковый id "c1"`},
		},
		{
			name:    "неизвестное поле пункта",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "count": 2, "match": {"by": "kind", "kind": "docx"}}]}`,
			wantErr: []string{"пункт состава #1", "структура пункта неверна", "count"},
		},
		{
			name:    "неизвестное поле match",
			content: `{"version": 1, "composition": [{"id": "c1", "title": "Заявка", "match": {"by": "kind", "kind": "docx", "regex": true}}]}`,
			wantErr: []string{"пункт состава #1", "структура пункта неверна", "regex"},
		},
		{
			name:    "пустой раздел composition — не ошибка",
			content: `{"version": 1, "composition": []}`,
			want:    Config{Version: SupportedVersion},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LoadConfig(writeRulesConfig(t, tc.content))
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Fatalf("LoadConfig: неожиданная ошибка: %v", err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("конфиг = %+v, хотели %+v", got, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("хотели ошибку, получили конфиг %+v", got)
			}
			if !errors.Is(err, ErrConfig) {
				t.Errorf("ошибка пункта состава обязана нести ErrConfig: %v", err)
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в ошибке нет %q: %v", want, err)
				}
			}
		})
	}
}

func TestSupportedVersion(t *testing.T) {
	// Контракт зафиксирован числом, а не константой: приёмка P2.1 требует
	// единственную версию схемы 1.
	if SupportedVersion != 1 {
		t.Errorf("SupportedVersion = %d, хотели 1", SupportedVersion)
	}
}

func TestFindingValidate(t *testing.T) {
	traced := trace.Ref{File: "пакет/заявка.docx", Format: trace.FormatDocx, Para: 3}
	// Файл без места и место без файла — оба Traced()==false: адрес указывает
	// в никуда с разных сторон.
	fileOnly := trace.Ref{File: "пакет/заявка.docx", Format: trace.FormatDocx}
	placeOnly := trace.Ref{Format: trace.FormatXlsx, Sheet: "ТЗ", Cell: "B7"}

	tests := []struct {
		name    string
		finding Finding
		wantErr []string
	}{
		{
			name:    "замечание без адресов",
			finding: Finding{Rule: "r1", Class: "test.class", Severity: SeverityBlocking, Text: "документ отсутствует в пакете"},
			wantErr: []string{"нет ни одного адреса", "документ отсутствует в пакете"},
		},
		{
			name:    "адреса есть, но ни один не трассируется",
			finding: Finding{Rule: "r1", Class: "test.class", Severity: SeverityWarning, Text: "реквизит", Refs: []trace.Ref{fileOnly, placeOnly}},
			wantErr: []string{"нет ни одного адреса", "реквизит"},
		},
		{
			name:    "хотя бы один полный адрес — замечание валидно",
			finding: Finding{Rule: "r1", Class: "test.class", Severity: SeverityBlocking, Text: "ок", Refs: []trace.Ref{fileOnly, traced}},
		},
		{
			name:    "severity отсутствует — замечание невалидно",
			finding: Finding{Rule: "r1", Class: "test.class", Text: "реквизит", Refs: []trace.Ref{traced}},
			wantErr: []string{"severity", "blocking или warning"},
		},
		{
			name:    "severity с опечаткой — замечание невалидно",
			finding: Finding{Rule: "r1", Class: "test.class", Severity: Severity("fatal"), Text: "реквизит", Refs: []trace.Ref{traced}},
			wantErr: []string{"severity", `"fatal"`, "blocking или warning"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.finding.Validate()
			if len(tc.wantErr) == 0 {
				if err != nil {
					t.Errorf("Validate: неожиданная ошибка: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate: хотели ошибку, получили nil")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("в ошибке нет %q: %v", want, err)
				}
			}
		})
	}
}
