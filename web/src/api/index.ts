// Единственная точка доступа к API из приложения (web/CLAUDE.md: только сгенерированный клиент,
// без ручных fetch). client.ts генерируется командой `npm run gen:api` из api/internal/docs/swagger.json —
// править его руками нельзя.
import { Api } from "./client";

export const api = new Api({
  // Тот же origin: фронтенд встроен в бинарник api; в dev Vite проксирует /api на 8100.
  baseUrl: "",
});

export type { HttpResponse, RequestParams } from "./client";
