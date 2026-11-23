package reqs

import (
	"testing"
	"time"
)

// Контрольные примеры реквизитов посчитаны по методикам ФНС (те же весовые
// коэффициенты, что в validate.go), а не взяты из документов: тест должен
// падать, если изменится алгоритм проверки, а не если изменится заявка.
const (
	testINN10  = "7714567895"
	testINN12  = "771456789041"
	testKPP    = "771401007"
	testOGRN   = "1027700014831"
	testOGRNIP = "304770000000008"
)

func TestValidateINN10(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		wantValid  bool
		wantReason string
	}{
		{"контрольная цифра сходится", testINN10, true, ""},
		{"последняя цифра сбита", "7714567894", false, "контрольная цифра не сходится"},
		{"не 10 цифр", "771456789", false, "не 10 цифр"},
		{"пустая строка", "", false, "не 10 цифр"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, reason := validateINN10(tt.value)
			if valid != tt.wantValid || reason != tt.wantReason {
				t.Errorf("validateINN10(%q) = (%v, %q), хотим (%v, %q)", tt.value, valid, reason, tt.wantValid, tt.wantReason)
			}
		})
	}
}

func TestValidateINN12(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		wantValid  bool
		wantReason string
	}{
		{"обе контрольные цифры сходятся", testINN12, true, ""},
		{"сбита 11-я", "771456789031", false, "11-я контрольная цифра не сходится"},
		{"сбита 12-я", "771456789040", false, "12-я контрольная цифра не сходится"},
		{"не 12 цифр", "7714567890", false, "не 12 цифр"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, reason := validateINN12(tt.value)
			if valid != tt.wantValid || reason != tt.wantReason {
				t.Errorf("validateINN12(%q) = (%v, %q), хотим (%v, %q)", tt.value, valid, reason, tt.wantValid, tt.wantReason)
			}
		})
	}
}

func TestValidateKPP(t *testing.T) {
	// У КПП нет контрольной цифры (последние три знака — порядковый номер
	// постановки), поэтому корректны и 781401001, и 482301001, и 771401009:
	// тест фиксирует именно это, чтобы проверка не вернулась к checksum.
	tests := []struct {
		name       string
		value      string
		wantValid  bool
		wantReason string
	}{
		{"КПП из пакета ТЭК СПб", "781401001", true, ""},
		{"КПП при ИНН 4823006703", "482301001", true, ""},
		{"последняя цифра любая", "771401009", true, ""},
		{"не 9 цифр", "77140100", false, "не 9 цифр"},
		{"пустая строка", "", false, "не 9 цифр"},
		{"код региона 00", "071401001", false, "код региона 00 не существует"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, reason := validateKPP(tt.value)
			if valid != tt.wantValid || reason != tt.wantReason {
				t.Errorf("validateKPP(%q) = (%v, %q), хотим (%v, %q)", tt.value, valid, reason, tt.wantValid, tt.wantReason)
			}
		})
	}
}

func TestValidateOGRN(t *testing.T) {
	tests := []struct {
		name       string
		validate   func(string) (bool, string)
		value      string
		wantValid  bool
		wantReason string
	}{
		{"ОГРН сходится", validateOGRN13, testOGRN, true, ""},
		{"ОГРН сбит контрольный разряд", validateOGRN13, "1027700014830", false, "ОГРН: контрольная цифра не сходится"},
		{"ОГРН первая цифра 8", validateOGRN13, "8462389547345", false, "ОГРН: недопустимая первая цифра 8"},
		{"ОГРН не 13 цифр", validateOGRN13, "102770001483", false, "ОГРН: не 13 цифр"},
		{"ОГРНИП сходится", validateOGRN15, testOGRNIP, true, ""},
		{"ОГРНИП первая цифра 1", validateOGRN15, "104567567567436", false, "ОГРНИП: недопустимая первая цифра 1"},
		{"ОГРНИП не 15 цифр", validateOGRN15, "30477000000000", false, "ОГРНИП: не 15 цифр"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, reason := tt.validate(tt.value)
			if valid != tt.wantValid || reason != tt.wantReason {
				t.Errorf("проверка %q = (%v, %q), хотим (%v, %q)", tt.value, valid, reason, tt.wantValid, tt.wantReason)
			}
		})
	}
}

func TestValidateDate(t *testing.T) {
	// Верхняя граница считается от текущего года (validateDate даёт запас в 15
	// лет на сроки действия доверенностей), поэтому пример не «протухает».
	lastPlausible := time.Now().UTC().Year() + 15
	tests := []struct {
		name  string
		year  int
		month int
		day   int
		valid bool
	}{
		{"обычная дата", 2026, 5, 12, true},
		{"29 февраля високосного года", 2024, 2, 29, true},
		{"29 февраля невисокосного года", 2025, 2, 29, false},
		{"31 апреля", 2026, 4, 31, false},
		{"месяц 13", 2026, 13, 1, false},
		{"день 0", 2026, 1, 0, false},
		{"год слишком старый", 1899, 1, 1, false},
		{"год на верхней границе запаса", lastPlausible, 1, 1, true},
		{"год за верхней границей запаса", lastPlausible + 1, 1, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, reason := validateDate(tt.year, tt.month, tt.day)
			if valid != tt.valid {
				t.Errorf("validateDate(%d-%02d-%02d) = (%v, %q), хотим valid=%v", tt.year, tt.month, tt.day, valid, reason, tt.valid)
			}
			if !tt.valid && reason == "" {
				t.Errorf("validateDate(%d-%02d-%02d) отклонена без причины", tt.year, tt.month, tt.day)
			}
		})
	}
}

func TestDaysInMonthAndLeap(t *testing.T) {
	tests := []struct {
		year, month, want int
		leap              bool
	}{
		{2024, 2, 29, true},
		{2025, 2, 28, false},
		{2026, 4, 30, false},
		{2026, 12, 31, false},
		{2000, 2, 29, true},
		{1900, 2, 28, false},
	}
	for _, tt := range tests {
		if got := daysInMonth(tt.year, tt.month); got != tt.want {
			t.Errorf("daysInMonth(%d, %d) = %d, хотим %d", tt.year, tt.month, got, tt.want)
		}
		if got := isLeap(tt.year); got != tt.leap {
			t.Errorf("isLeap(%d) = %v, хотим %v", tt.year, got, tt.leap)
		}
	}
}

func TestNormalizeCurrency(t *testing.T) {
	tests := []struct{ mark, want string }{
		{"руб.", "RUB"},
		{"рублей", "RUB"},
		{"RUB", "RUB"},
		{"₽", "RUB"},
		{"USD", "USD"},
		{"$", "USD"},
		{"долларов", "USD"},
		{"доллара", "USD"},
		{"EUR", "EUR"},
		{"€", "EUR"},
		{"  руб  ", "RUB"},
		{"BYN", "BYN"},
	}
	for _, tt := range tests {
		if got := normalizeCurrency(tt.mark); got != tt.want {
			t.Errorf("normalizeCurrency(%q) = %q, хотим %q", tt.mark, got, tt.want)
		}
	}
}

// TestExportedValidators — экспортированные обёртки P2.3 (конфиг эталонов
// проверяет реквизиты через них) выбирают валидатор по длине и возвращают
// тот же результат, что и внутренние функции.
func TestExportedValidators(t *testing.T) {
	tests := []struct {
		name      string
		check     func(string) (bool, string)
		value     string
		wantValid bool
	}{
		{"ValidateINN: юрлицо валидно", ValidateINN, testINN10, true},
		{"ValidateINN: ИП валидно", ValidateINN, testINN12, true},
		{"ValidateINN: неверная длина", ValidateINN, "12345", false},
		{"ValidateINN: контрольная цифра сбита", ValidateINN, "7714567894", false},
		{"ValidateKPP: валидно", ValidateKPP, testKPP, true},
		{"ValidateKPP: не 9 цифр", ValidateKPP, "77140100", false},
		{"ValidateOGRN: ОГРН валидно", ValidateOGRN, testOGRN, true},
		{"ValidateOGRN: ОГРНИП валидно", ValidateOGRN, testOGRNIP, true},
		{"ValidateOGRN: неверная длина", ValidateOGRN, "10277000148", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, _ := tt.check(tt.value)
			if valid != tt.wantValid {
				t.Errorf("check(%q) валидность = %v, хотим %v", tt.value, valid, tt.wantValid)
			}
		})
	}
}
