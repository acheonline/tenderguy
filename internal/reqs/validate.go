package reqs

import (
	"strconv"
	"time"
)

// Валидация реквизитов — то, чем P1.5 отличается от «найти цифры регэкспом»:
// контрольные числа по методикам ФНС и календарная корректность дат.

// ValidateINN проверяет ИНН юрлица (10 цифр) или физлица/ИП (12 цифр).
// Экспортирован для P2.3: конфигурация эталонных реквизитов обязана проходить
// ту же проверку, что и значения из документов, иначе опечатка в эталоне
// превратится в ложные блокирующие замечания по всему пакету.
func ValidateINN(s string) (bool, string) {
	switch len(s) {
	case 10:
		return validateINN10(s)
	case 12:
		return validateINN12(s)
	}
	return false, "не 10 цифр (юрлицо) и не 12 цифр (ИП)"
}

// ValidateKPP проверяет КПП: 9 цифр, код региона не 00.
func ValidateKPP(s string) (bool, string) {
	return validateKPP(s)
}

// ValidateOGRN проверяет ОГРН (13 цифр) или ОГРНИП (15 цифр).
func ValidateOGRN(s string) (bool, string) {
	switch len(s) {
	case 13:
		return validateOGRN13(s)
	case 15:
		return validateOGRN15(s)
	}
	return false, "не 13 цифр (ОГРН) и не 15 цифр (ОГРНИП)"
}

// validateINN10 — контрольная цифра юрлица: взвешенная сумма первых 9 цифр
// по коэффициентам 2,4,10,3,5,9,4,6,8, остаток от деления на 11, затем на 10.
func validateINN10(s string) (bool, string) {
	d := digits(s)
	if len(d) != 10 {
		return false, "не 10 цифр"
	}
	w := []int{2, 4, 10, 3, 5, 9, 4, 6, 8}
	sum := 0
	for i, m := range w {
		sum += m * d[i]
	}
	if sum%11%10 != d[9] {
		return false, "контрольная цифра не сходится"
	}
	return true, ""
}

// validateINN12 — ИНН физлица/ИП: две контрольные цифры (11-я по первым 10,
// 12-я по первым 11).
func validateINN12(s string) (bool, string) {
	d := digits(s)
	if len(d) != 12 {
		return false, "не 12 цифр"
	}
	w11 := []int{7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
	sum11 := 0
	for i, m := range w11 {
		sum11 += m * d[i]
	}
	if sum11%11%10 != d[10] {
		return false, "11-я контрольная цифра не сходится"
	}
	w12 := []int{3, 7, 2, 4, 10, 3, 5, 9, 4, 6, 8}
	sum12 := 0
	for i, m := range w12 {
		sum12 += m * d[i]
	}
	if sum12%11%10 != d[11] {
		return false, "12-я контрольная цифра не сходится"
	}
	return true, ""
}

// validateKPP — КПП: 9 цифр вида NNNN PP XXX, где NNNN — код налогового органа
// (код региона + номер инспекции), PP — причина постановки на учёт, XXX —
// порядковый номер постановки. Контрольной цифры у КПП нет (в отличие от ИНН и
// ОГРН), поэтому проверяется только разрядность и код региона: первые две
// цифры не могут быть 00.
//
// Проверять КПП «весами» 7,3,9,5,1… нельзя: на реальных значениях из пакетов
// (781401001 поставщика ТЭК СПб, 745301001 и 164401001 в договорах НЭО) такая
// формула не сходится, тогда как алгоритм ИНН на тех же документах сходится —
// то есть проверка помечала бы корректные КПП как подозрительные.
func validateKPP(s string) (bool, string) {
	if len(s) != 9 {
		return false, "не 9 цифр"
	}
	if s[0] == '0' {
		return false, "код региона 00 не существует"
	}
	return true, ""
}

// validateOGRN13 — ОГРН: последняя цифра — единицы остатка от деления на 11
// удвоенного числа из первых 12 цифр. Первая цифра 1–5.
func validateOGRN13(s string) (bool, string) {
	return validateOGRN(s, 13, "ОГРН")
}

// validateOGRN15 — ОГРНИП: то же правило для 14 первых цифр, первая — 3 или 4.
func validateOGRN15(s string) (bool, string) {
	return validateOGRN(s, 15, "ОГРНИП")
}

func validateOGRN(s string, total int, label string) (bool, string) {
	if len(s) != total {
		return false, label + ": не " + strconv.Itoa(total) + " цифр"
	}
	first := s[0] - '0'
	allowed := first >= 1 && first <= 5
	if total == 15 {
		allowed = first == 3 || first == 4
	}
	if !allowed {
		return false, label + ": недопустимая первая цифра " + string(rune(s[0]))
	}
	head, err := strconv.ParseUint(s[:total-1], 10, 64)
	if err != nil {
		return false, label + ": " + err.Error()
	}
	check := int((head*2)%11) % 10
	if check != int(s[total-1]-'0') {
		return false, label + ": контрольная цифра не сходится"
	}
	return true, ""
}

// validateDate — календарь и разумные границы: год не раньше 1900-х и не
// позже текущего + 15 (заявочные сроки действия доверенностей/лицензий).
func validateDate(year, month, day int) (bool, string) {
	limit := time.Now().UTC().Year() + 15
	if year < 1900 || year > limit {
		return false, "год вне разумных границ"
	}
	if month < 1 || month > 12 {
		return false, "месяц вне 1..12"
	}
	if day < 1 || day > daysInMonth(year, month) {
		return false, "день больше длины месяца"
	}
	return true, ""
}

func daysInMonth(year, month int) int {
	switch month {
	case 4, 6, 9, 11:
		return 30
	case 2:
		if isLeap(year) {
			return 29
		}
		return 28
	default:
		return 31
	}
}

func isLeap(year int) bool {
	return year%4 == 0 && (year%100 != 0 || year%400 == 0)
}

// digits — цифры строки; нецифровой символ отсекает разбор (значение уже
// извлечено по \b\d+\b, так что нецифр здесь не бывает).
func digits(s string) []int {
	out := make([]int, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return nil
		}
		out = append(out, int(s[i]-'0'))
	}
	return out
}
