package report

import (
	"encoding/csv"
	"io"
	"strings"
	"time"

	"github.com/jack5505/taskflow/api/internal/duration"
)

// bom — метка UTF-8: без неё русский Excel читает файл в cp1251 (§6.6).
const bom = "\uFEFF"

// deletedMark — пометка записей удалённой задачи (§4).
const deletedMark = "удалённая задача"

// statusLabels — названия статусов как в веб-интерфейсе (§4, §7.3).
var statusLabels = map[string]string{
	"todo":        "К выполнению",
	"in_progress": "В работе",
	"review":      "На проверке",
	"done":        "Готово",
	"cancelled":   "Отменена",
}

// WriteCSV — отчёт в формате для русского Excel (§6.6): UTF-8 с BOM, разделитель «;», строки
// через CRLF, часы с десятичной запятой (2,50), даты dd.mm.yyyy. Кавычки и переводы строк в
// значениях экранирует encoding/csv (поле в кавычках, кавычка удваивается).
func WriteCSV(out io.Writer, r *Report) error {
	if _, err := io.WriteString(out, bom); err != nil {
		return err
	}
	w := csv.NewWriter(out)
	w.Comma, w.UseCRLF = ';', true
	var rows [][]string
	if r.GroupBy == GroupByTask {
		rows = taskRows(r)
	} else {
		rows = userRows(r)
	}
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	return w.Error()
}

func userRows(r *Report) [][]string {
	head := []string{"Пользователь"}
	for _, d := range r.Days {
		head = append(head, ruDate(d.Date))
	}
	rows := [][]string{append(head, "Итого")}
	for _, u := range r.Users {
		row := []string{text(u.UserName)}
		for _, m := range u.Minutes {
			row = append(row, hours(m))
		}
		rows = append(rows, append(row, hours(u.TotalMinutes)))
	}
	foot := []string{"Итого"}
	for _, m := range r.DayTotals {
		foot = append(foot, hours(m))
	}
	return append(rows, append(foot, hours(r.TotalMinutes)))
}

func taskRows(r *Report) [][]string {
	rows := [][]string{{"Ключ", "Название", "Исполнитель", "Статус", "Оценка, ч",
		"Списано за период, ч", "Списано всего, ч", "Пометка"}}
	for _, t := range r.Tasks {
		assignee, estimate, mark := "", "", ""
		if t.AssigneeName != nil {
			assignee = text(*t.AssigneeName)
		}
		if t.EstimateMinutes != nil {
			estimate = hours(*t.EstimateMinutes)
		}
		if t.Deleted {
			mark = deletedMark
		}
		status := statusLabels[t.Status]
		if status == "" {
			status = t.Status
		}
		rows = append(rows, []string{t.Key, text(t.Title), assignee, status, estimate,
			hours(t.PeriodMinutes), hours(t.TotalMinutes), mark})
	}
	return append(rows, []string{"Итого", "", "", "", "", hours(r.TotalMinutes), "", ""})
}

// hours — десятичные часы §6.1 с запятой: 150 → «2,50».
func hours(m int) string { return strings.Replace(duration.Hours(m), ".", ",", 1) }

// ruDate — ГГГГ-ММ-ДД → дд.мм.гггг.
func ruDate(d string) string {
	t, err := time.Parse(time.DateOnly, d)
	if err != nil {
		return d
	}
	return t.Format("02.01.2006")
}

// text — пользовательский текст в ячейке. Начало с = + - @ (и табуляции, перевода строки)
// Excel принимает за формулу; апостроф перед ним — общепринятая защита от CSV-инъекции.
func text(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// FileName — имя файла выгрузки: time-report-user-2026-10-05_2026-10-11.csv.
func FileName(r *Report) string {
	return "time-report-" + r.GroupBy + "-" + r.From + "_" + r.To + ".csv"
}
