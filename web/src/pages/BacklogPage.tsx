import { Link, useParams } from "react-router";

// Заглушка страницы «Бэклог и спринты» (ТЗ §7.4) — отдельная задача; сюда ведёт пустая доска.
export function BacklogPage() {
  const { key } = useParams();
  return (
    <section>
      <h2>Бэклог проекта {key}</h2>
      <p className="muted">Бэклог и спринты появятся здесь.</p>
      <Link to={`/projects/${key}`}>← К доске</Link>
    </section>
  );
}
