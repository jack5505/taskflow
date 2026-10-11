import { describe, expect, it, vi } from "vitest";
import { createAuthFetch } from "./authFetch";

function setup(opts: { refreshTo?: string | null; token?: string | null } = {}) {
  let token: string | null = opts.token ?? "old";
  const calls: { url: string; auth: string | null }[] = [];
  let release!: () => void;
  const gate = new Promise<void>((r) => (release = r));

  const baseFetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const auth = new Headers(init?.headers).get("Authorization");
    calls.push({ url: String(input), auth });
    return new Response(null, { status: auth === "Bearer new" ? 200 : 401 });
  });
  const refresh = vi.fn(async () => {
    await gate;
    return opts.refreshTo === undefined ? "new" : opts.refreshTo;
  });
  const onRefreshed = vi.fn((t: string | null) => {
    token = t;
  });
  const af = createAuthFetch({ baseFetch, getToken: () => token, refresh, onRefreshed });
  const call = (url = "/api/v1/me", auth: string | null = `Bearer ${token}`) =>
    af.fetch(url, { headers: auth ? { Authorization: auth } : {} });
  return { af, call, calls, refresh, onRefreshed, release, setToken: (t: string) => (token = t) };
}

describe("createAuthFetch", () => {
  it("на 401 обновляет токен и повторяет запрос с новым", async () => {
    const s = setup();
    s.release();
    const r = await s.call();
    expect(r.status).toBe(200);
    expect(s.refresh).toHaveBeenCalledTimes(1);
    expect(s.onRefreshed).toHaveBeenCalledWith("new");
    expect(s.calls.map((c) => c.auth)).toEqual(["Bearer old", "Bearer new"]);
  });

  it("параллельные 401 делят один refresh", async () => {
    const s = setup();
    const all = Promise.all([s.call("/a"), s.call("/b"), s.call("/c")]);
    await vi.waitFor(() => expect(s.calls).toHaveLength(3));
    s.release();
    const rs = await all;
    expect(rs.map((r) => r.status)).toEqual([200, 200, 200]);
    expect(s.refresh).toHaveBeenCalledTimes(1);
    expect(s.onRefreshed).toHaveBeenCalledTimes(1);
  });

  it("неудачный refresh возвращает исходный 401 и сообщает null", async () => {
    const s = setup({ refreshTo: null });
    s.release();
    const r = await s.call();
    expect(r.status).toBe(401);
    expect(s.onRefreshed).toHaveBeenCalledWith(null);
    expect(s.calls).toHaveLength(1);
  });

  it("исключение в refresh считается неудачей", async () => {
    const s = setup();
    s.refresh.mockRejectedValueOnce(new Error("сеть"));
    const r = await s.call();
    expect(r.status).toBe(401);
    expect(s.onRefreshed).toHaveBeenCalledWith(null);
  });

  it("запрос без Authorization (вход) не обновляется", async () => {
    const s = setup();
    const r = await s.call("/api/v1/auth/login", null);
    expect(r.status).toBe(401);
    expect(s.refresh).not.toHaveBeenCalled();
  });

  it("если токен уже обновили, повторяет без нового refresh", async () => {
    const s = setup();
    s.setToken("new");
    const r = await s.call("/api/v1/me", "Bearer old");
    expect(r.status).toBe(200);
    expect(s.refresh).not.toHaveBeenCalled();
  });

  it("после завершения refresh следующий 401 запускает новый", async () => {
    const s = setup({ refreshTo: null });
    s.release();
    await s.call();
    s.setToken("old");
    await s.call();
    expect(s.refresh).toHaveBeenCalledTimes(2);
  });
});
