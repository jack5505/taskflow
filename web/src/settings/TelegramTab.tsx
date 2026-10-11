import type { ProjectView } from "../api/client";

/**
 * Telegram (§7.9, §8.2). Выдачи кода подключения и отключения группы в API пока нет
 * (POST /projects/{key}/telegram-bind-code, DELETE /projects/{key}/telegram), поэтому вкладка
 * показывает состояние и порядок подключения — само подключение выполняется в Telegram.
 */
export function TelegramTab({ project }: { project: ProjectView }) {
  const chat = project.telegram_chat_id;
  return (
    <section className="panel settings-text">
      <h3>Telegram-группа</h3>
      {chat ? (
        <p>
          Группа подключена (id чата <span className="card-key">{chat}</span>).
        </p>
      ) : (
        <p className="muted">Группа не подключена.</p>
      )}
      <h4>Как подключить группу</h4>
      <ol>
        <li>Получите одноразовый код подключения группы к проекту {project.key} (8 символов, действует 15 минут).</li>
        <li>Добавьте бота в группу Telegram — права администратора группы ему не нужны.</li>
        <li>
          Напишите в группе <code>/bind &lt;код&gt;</code>. Команду может отправить только менеджер этого проекта с
          привязанным в профиле Telegram.
        </li>
        <li>Бот ответит «Группа подключена к проекту…». У проекта может быть только одна группа.</li>
      </ol>
      <p className="notice">
        Выдача кода в веб-интерфейсе пока недоступна: подключение и отключение выполняются в Telegram. Отключить группу
        — команда <code>/unbind</code> в группе (если в ней несколько проектов — <code>/unbind {project.key}</code>).
      </p>
    </section>
  );
}
