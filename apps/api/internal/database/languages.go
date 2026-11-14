package database

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/language"
)

type Language struct{ Code, Name, NativeName, Direction string }

const directionLTR = "ltr"

// ReadLanguages проверяет весь UTF-8 CSV до записи. Поле is_enabled отсутствует:
// импорт метаданных не должен активировать язык.
func ReadLanguages(reader io.Reader) ([]Language, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, 8*1024*1024+1))
	if err != nil {
		return nil, fmt.Errorf("чтение CSV: %w", err)
	}
	if len(raw) > 8*1024*1024 {
		return nil, fmt.Errorf("CSV превышает 8 MiB")
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("CSV должен быть UTF-8")
	}
	r := csv.NewReader(strings.NewReader(string(raw)))
	r.FieldsPerRecord = 4
	header, err := r.Read()
	if err != nil || strings.Join(header, ",") != "code,name,native_name,direction" {
		return nil, fmt.Errorf("CSV: ожидается заголовок code,name,native_name,direction")
	}
	seen := make(map[string]bool)
	var result []Language
	for line := 2; ; line++ {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("CSV строка %d: неверный формат", line)
		}
		row, err := readLanguageRecord(record, line, seen)
		if err != nil {
			return nil, err
		}
		seen[row.Code] = true
		result = append(result, row)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("CSV не содержит языков")
	}
	return result, nil
}

func readLanguageRecord(record []string, line int, seen map[string]bool) (Language, error) {
	for i := range record {
		record[i] = strings.TrimSpace(record[i])
	}
	if strings.ContainsAny(record[0], "_@. ") {
		return Language{}, fmt.Errorf("CSV строка %d: некорректный BCP 47 код", line)
	}
	tag, err := language.Parse(record[0])
	if err != nil || record[0] == "" {
		return Language{}, fmt.Errorf("CSV строка %d: некорректный BCP 47 код", line)
	}
	code := strings.ToLower(tag.String())
	if len(code) < 2 || len(code) > 63 || seen[code] {
		return Language{}, fmt.Errorf("CSV строка %d: длина кода или повтор языка", line)
	}
	if record[1] == "" || record[2] == "" || strings.ContainsRune(strings.Join(record, ""), 0) || (record[3] != directionLTR && record[3] != "rtl") {
		return Language{}, fmt.Errorf("CSV строка %d: обязательные имена и направление ltr/rtl", line)
	}
	return Language{code, record[1], record[2], record[3]}, nil
}

func Seed(ctx context.Context, db *sql.DB) error {
	return ImportLanguages(ctx, db, []Language{
		{"ru", "Russian", "Русский", directionLTR}, {"pl", "Polish", "Polski", directionLTR},
		{"kk", "Kazakh", "Қазақша", directionLTR}, {"en", "English", "English", directionLTR},
	})
}

// ImportLanguages записывает проверенный файл атомарно. Сортировка UPSERT
// согласует порядок блокировок строк каталога с lock_languages.
func ImportLanguages(ctx context.Context, db *sql.DB, rows []Language) error {
	ordered := append([]Language(nil), rows...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Code < ordered[j].Code })
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "SET LOCAL ROLE sp_language_importer"); err != nil {
		return err
	}
	for _, row := range ordered {
		_, err = tx.ExecContext(ctx, `INSERT INTO public.languages(code,name,native_name,direction) VALUES($1,$2,$3,$4)
   ON CONFLICT(code) DO UPDATE SET name=EXCLUDED.name,native_name=EXCLUDED.native_name,direction=EXCLUDED.direction
   WHERE (languages.name,languages.native_name,languages.direction) IS DISTINCT FROM (EXCLUDED.name,EXCLUDED.native_name,EXCLUDED.direction)`, row.Code, row.Name, row.NativeName, row.Direction)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}
