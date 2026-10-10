import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import "./App.css";

type Health = { status: string; checks: Record<string, string> };

// /health отвечает 200 или 503 с одинаковым телом: 503 — это ответ, а не сбой запроса.
async function fetchHealth({ signal }: { signal: AbortSignal }): Promise<Health> {
  const r = await api.health.healthList({ signal });
  if (r.ok) return r.data as Health;
  if (r.error && typeof r.error === "object" && "checks" in r.error) return r.error as Health;
  throw new Error(`Сервис недоступен (${r.status})`);
}

// Стартовая страница до появления экранов из ТЗ §7: показывает состояние сервиса
// через сгенерированный клиент (ТЗ §10.3).
function App() {
  const { data: health, error, isPending } = useQuery({ queryKey: ["health"], queryFn: fetchHealth });

  return (
    <main>
      <h1>TaskFlow</h1>
      {isPending && <p>Проверяем состояние сервиса…</p>}
      {error && <p role="alert">{error.message}</p>}
      {health && (
        <section>
          <p>
            Состояние: <strong>{health.status === "ok" ? "в порядке" : "есть сбои"}</strong>
          </p>
          <ul>
            {Object.entries(health.checks).map(([name, state]) => (
              <li key={name}>
                {name}: {state}
              </li>
            ))}
          </ul>
        </section>
      )}
      <p>
        <a href="/api/docs">Документация API</a>
      </p>
    </main>
  );
}

export default App;
