import { request } from "@playwright/test";
import { ADMIN_EMAIL, ADMIN_LINK, ADMIN_PASSWORD, BASE_URL } from "./env.ts";

// Задаёт пароль администратору, от имени которого тесты готовят данные, и проверяет вход.
// Ссылка уже погашена (повторный прогон на том же стенде) — пароль задан прошлым прогоном.
// Вход до установки пароля не пробуем: неудачные попытки копятся до блокировки (§10.3).
export default async function globalSetup() {
  const ctx = await request.newContext({ baseURL: BASE_URL });
  try {
    let setError = "";
    if (ADMIN_LINK) {
      const token = new URL(ADMIN_LINK).searchParams.get("token");
      const r = await ctx.post("/api/v1/auth/password-set", { data: { token, password: ADMIN_PASSWORD } });
      if (!r.ok()) setError = ` (password-set: ${r.status()} ${await r.text()})`;
    }
    const r = await ctx.post("/api/v1/auth/login", { data: { email: ADMIN_EMAIL, password: ADMIN_PASSWORD } });
    if (!r.ok())
      throw new Error(`Вход администратора E2E: ${r.status()} ${await r.text()}${setError}. Стенд поднят через e2e/stack.sh up?`);
  } finally {
    await ctx.dispose();
  }
}
