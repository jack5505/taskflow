import { useEffect, useState } from "react";
import { api } from "./api";
import "./App.css";

type Health = { status: string; checks: Record<string, string> };

// Стартовая страница до появления экранов из ТЗ §7: показывает состояние сервиса
// через сгенерированный клиент (ТЗ §10.3, /health).
function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api.health
      .healthList()
      .then((r) => setHealth(r.data as Health))
      .catch((e: { error?: Health; status?: number }) => {
        // 503 приходит с тем же телом: сервис деградировал, но отвечает.
        if (e.error && "checks" in e.error) {
          setHealth(e.error);
        } else {
          setError(`Сервис недоступен (${e.status ?? "нет ответа"})`);
        }
      });
  }, []);

  return (
    <main>
      <h1>TaskFlow</h1>
      {error && <p role="alert">{error}</p>}
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
        <a href="/api/docs/">Документация API</a>
      </p>
    </main>
  );
}

export default App;
