package rules

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
