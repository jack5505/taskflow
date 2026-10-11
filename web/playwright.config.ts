import { defineConfig, devices } from "@playwright/test";
import { BASE_URL } from "./e2e/env.ts";

// E2E-сценарии приёмки ТЗ §10.4 (issue #46). Стенд поднимает e2e/stack.sh, адрес и ссылки
// установки пароля приходят в переменных E2E_* (см. e2e/env.ts).
export default defineConfig({
  testDir: "./e2e",
  testMatch: "*.e2e.ts",
  globalSetup: "./e2e/global-setup.ts",
  // Сценарии независимы (у каждого свой проект), но таймер у пользователя один на всех —
  // один поток и повторы при сбое, чтобы обязательная проверка не мигала.
  workers: 1,
  retries: process.env.CI ? 2 : 0,
  forbidOnly: !!process.env.CI,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? [["list"], ["html", { open: "never" }]] : [["list"]],
  use: {
    baseURL: BASE_URL,
    locale: "ru-RU",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 900 } } }],
});
