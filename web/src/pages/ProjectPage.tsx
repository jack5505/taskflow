import { Link, useParams } from "react-router";

// Заглушка страницы проекта: доска (ТЗ §7.3) — задача #38.
export function ProjectPage() {
  const { key } = useParams();
  return (
    <section>
      <h2>Проект {key}</h2>
      <p className="muted">Доска проекта появится здесь.</p>
      <Link to="/">← К списку проектов</Link>
    </section>
  );
}
