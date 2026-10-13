// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
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
  beforeEach(() => {
    vi.useFakeTimers();
    host = document.createElement("div");
    document.body.append(host);
    act(() =>
      createRoot(host).render(
        <ToastProvider>
          <Grab />
        </ToastProvider>,
      ),
    );
  });
  afterEach(() => {
    vi.useRealTimers();
    host.remove();
  });

  const texts = (role: string) => [...host.querySelectorAll(`[role="${role}"] .toast-text`)].map((e) => e.textContent);

  it("успех — в вежливой области, ошибка — в срочной; сами исчезают", () => {
    act(() => toast.success("Сохранено"));
    act(() => toast.error("Нет прав"));
    expect(texts("status")).toEqual(["Сохранено"]);
    expect(texts("alert")).toEqual(["Нет прав"]);
    act(() => vi.advanceTimersByTime(TOAST_MS + 1));
    expect(texts("status")).toEqual([]);
    expect(texts("alert")).toEqual([]);
  });

  it("не больше трёх сразу: старые уходят", () => {
    act(() => ["1", "2", "3", "4"].forEach((t) => toast.success(t)));
    expect(texts("status")).toEqual(["2", "3", "4"]);
  });

  it("действие выполняется и закрывает сообщение; крестик закрывает", () => {
    const undo = vi.fn();
    act(() => toast.success("Задача удалена", { label: "Отменить", onClick: undo }));
    const action = host.querySelector<HTMLButtonElement>(".toast-action")!;
    expect(action.textContent).toBe("Отменить");
    act(() => action.click());
    expect(undo).toHaveBeenCalledOnce();
    expect(texts("status")).toEqual([]);

    act(() => toast.success("Ещё"));
    act(() => host.querySelector<HTMLButtonElement>(".toast-close")!.click());
    expect(texts("status")).toEqual([]);
  });
});
