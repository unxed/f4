package sqlite

func browserHelpTitle() string {
	return sqliteText("SQLite.HelpTitle", "SQLite browser", "Обозреватель SQLite")
}

func browserHelpText() string {
	return sqliteText("SQLite.Help",
		"Browse the database tables and views on the left; the selected table's rows appear on the right.\n\n"+
			"- **Enter** on a table: show that table.\n"+
			"- **Enter** on a result cell or **F4**: edit the selected cell in a browsed table.\n"+
			"- **Insert**: add a row to the browsed table.\n"+
			"- **Delete**: delete the selected row after confirmation.\n"+
			"- **Ctrl+PgUp / Ctrl+PgDn**: browse the previous or next 100 rows.\n"+
			"- **F9** or **Run**: execute the SQL in the editor. Results from a typed query are read-only.\n"+
			"- **Refresh**: reload the database schema and current table. **Esc** or **Close**: close the browser.",
		"Слева перечислены таблицы и представления базы, справа — строки выбранной таблицы.\n\n"+
			"- **Enter** на таблице: показать её.\n"+
			"- **Enter** на ячейке результата или **F4**: изменить выбранную ячейку просматриваемой таблицы.\n"+
			"- **Insert**: добавить строку в просматриваемую таблицу.\n"+
			"- **Delete**: удалить выбранную строку после подтверждения.\n"+
			"- **Ctrl+PgUp / Ctrl+PgDn**: перейти на 100 строк назад или вперёд.\n"+
			"- **F9** или **Выполнить**: выполнить SQL из редактора. Результаты произвольного запроса доступны только для чтения.\n"+
			"- **Обновить**: перечитать схему базы и текущую таблицу. **Esc** или **Закрыть**: закрыть обозреватель.")
}
