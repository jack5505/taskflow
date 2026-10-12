package task

import (
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/jack5505/taskflow/api/internal/user"
)

// fillNames дописывает в строки rows имена пользователей: id берёт get (nil — нет пользователя),
// имя записывает set. Пользователя, которого нет в базе, set не получает.
func fillNames[T any](db *gorm.DB, rows []T, get func(*T) *uuid.UUID, set func(*T, string)) error {
	var ids []uuid.UUID
	for i := range rows {
		if id := get(&rows[i]); id != nil {
			ids = append(ids, *id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	names, err := user.Names(db, ids...)
	if err != nil {
		return err
	}
	for i := range rows {
		if id := get(&rows[i]); id != nil {
			if n, ok := names[*id]; ok {
				set(&rows[i], n.DisplayName)
			}
		}
	}
	return nil
}
