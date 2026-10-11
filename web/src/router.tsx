import { createBrowserRouter, Navigate } from "react-router";
import { RequireAuth, RequireGuest } from "./auth/guards";
import { AppLayout } from "./pages/AppLayout";
import { AuthLayout } from "./pages/AuthLayout";
import { ForgotPasswordPage } from "./pages/ForgotPasswordPage";
import { HomePage } from "./pages/HomePage";
import { LoginPage } from "./pages/LoginPage";
import { PasswordSetPage } from "./pages/PasswordSetPage";

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
        children: [{ path: "/", element: <HomePage /> }],
      },
    ],
  },
  { path: "*", element: <Navigate to="/" replace /> },
]);
