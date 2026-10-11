import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "react-router";
import { refreshSession } from "./api";
import { parseApiError } from "./api/errors";
import { subscribe, getStatus } from "./auth/session";
import { router } from "./router";
import "./index.css";

// Состояние сервера живёт в TanStack Query (web/CLAUDE.md): кэш, повторы и отмена запросов — его забота.
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Ошибки 4xx повтором не исправить; 401 уже обработан автообновлением токена.
      retry: (count, e) => {
        const status = parseApiError(e)?.status;
        return count < 1 && (status === undefined || status >= 500);
      },
      refetchOnWindowFocus: false,
    },
  },
});

// Сессия закончилась (выход или неудачный refresh) — данные прежнего пользователя не держим.
subscribe(() => {
  if (getStatus() === "out") queryClient.clear();
});

// Access-токен живёт только в памяти, поэтому после перезагрузки страницы восстанавливаем
// сессию по refresh-cookie. Пока ответа нет, закрытые страницы показывают «Загрузка…».
void refreshSession();

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
);
