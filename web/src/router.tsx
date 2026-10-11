import { createBrowserRouter, Navigate } from "react-router";
import { RequireAuth, RequireGuest } from "./auth/guards";
import { AppLayout } from "./pages/AppLayout";
import { AuthLayout } from "./pages/AuthLayout";
import { ForgotPasswordPage } from "./pages/ForgotPasswordPage";
import { HomePage } from "./pages/HomePage";
import { LoginPage } from "./pages/LoginPage";
import { PasswordSetPage } from "./pages/PasswordSetPage";
import { BacklogPage } from "./pages/BacklogPage";
import { BoardPage } from "./board/BoardPage";
import { SettingsPage } from "./settings/SettingsPage";

// Адреса страниц. /password-set — путь из письма (api/internal/auth: baseURL + /password-set?token=…).
export const router = createBrowserRouter([
  {
    element: <AuthLayout />,
    children: [
      {
        element: <RequireGuest />,
        children: [
          { path: "/login", element: <LoginPage /> },
          { path: "/forgot-password", element: <ForgotPasswordPage /> },
        ],
      },
      // Доступна и с открытой сессией: ссылку из письма могут открыть в любом состоянии.
      { path: "/password-set", element: <PasswordSetPage /> },
    ],
  },
  {
    element: <RequireAuth />,
    children: [
      {
        element: <AppLayout />,
        children: [
          { path: "/", element: <HomePage /> },
          { path: "/projects/:key", element: <BoardPage /> },
          { path: "/projects/:key/backlog", element: <BacklogPage /> },
          { path: "/projects/:key/settings", element: <SettingsPage /> },
          // Карточка с Markdown (marked + DOMPurify) — отдельным чанком: на остальных экранах не нужна.
          { path: "/tasks/:key", lazy: async () => ({ Component: (await import("./task/TaskPage")).TaskPage }) },
        ],
      },
    ],
  },
  { path: "*", element: <Navigate to="/" replace /> },
]);
