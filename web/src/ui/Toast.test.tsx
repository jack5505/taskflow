// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { TOAST_MS, ToastProvider, useToast, type Toasts } from "./Toast";

// React без testing-library: act + createRoot.
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let toast: Toasts;
function Grab() {
  toast = useToast();
  return null;
}

describe("ToastProvider", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    vi.useFakeTimers();
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
    act(() =>
      root.render(
        <ToastProvider>
          <Grab />
        </ToastProvider>,
      ),
    );
  });
  afterEach(() => {
    act(() => root.unmount());
    vi.useRealTimers();
    host.remove();
  });

  // Сообщения рисуются порталом в document.body (или в открытое модальное окно).
  const texts = (role: string) => [...document.body.querySelectorAll(`[role="${role}"] .toast-text`)].map((e) => e.textContent);

  it("успех — в вежливой области, ошибка — в срочной; сами исчезают", () => {
    act(() => toast.success("Сохранено"));
    act(() => toast.error("Нет прав"));
    expect(texts("status")).toEqual(["Сохранено"]);
    expect(texts("alert")).toEqual(["Нет прав"]);
    act(() => vi.advanceTimersByTime(TOAST_MS + 1));
    expect(texts("status")).toEqual([]);
    expect(texts("alert")).toEqual([]);
  });

  it("не больше трёх каждого вида: старые уходят, успехи не вытесняют ошибку", () => {
    act(() => toast.error("Ошибка"));
    act(() => ["1", "2", "3", "4"].forEach((t) => toast.success(t)));
    expect(texts("status")).toEqual(["2", "3", "4"]);
    expect(texts("alert")).toEqual(["Ошибка"]);
  });

  it("действие выполняется и закрывает сообщение; крестик закрывает", () => {
    const undo = vi.fn();
    act(() => toast.success("Задача удалена", { label: "Отменить", onClick: undo }));
    const action = document.body.querySelector<HTMLButtonElement>(".toast-action")!;
    expect(action.textContent).toBe("Отменить");
    act(() => action.click());
    expect(undo).toHaveBeenCalledOnce();
    expect(texts("status")).toEqual([]);

    act(() => toast.success("Ещё"));
    act(() => document.body.querySelector<HTMLButtonElement>(".toast-close")!.click());
    expect(texts("status")).toEqual([]);
  });
});
