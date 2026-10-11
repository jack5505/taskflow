import { describe, expect, it, vi } from "vitest";
import { createAuthFetch } from "./authFetch";

function setup(opts: { refreshTo?: string | null; token?: string | null } = {}) {
  let token: string | null = opts.token ?? "old";
  let epoch = 0;
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
  const af = createAuthFetch({ baseFetch, getToken: () => token, getEpoch: () => epoch, refresh, onRefreshed });
  const call = (url = "/api/v1/me", auth: string | null = `Bearer ${token}`) =>
    af.fetch(url, { headers: auth ? { Authorization: auth } : {} });
  const signOut = () => {
    epoch++;
    token = null;
  };
  return { af, call, calls, refresh, onRefreshed, release, signOut, setToken: (t: string) => (token = t) };
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

  it("временный сбой refresh не завершает сессию", async () => {
    const s = setup();
    s.refresh.mockRejectedValueOnce(new Error("сеть"));
    const r = await s.call();
    expect(r.status).toBe(401);
    expect(s.onRefreshed).not.toHaveBeenCalled();
    s.release();
    await expect(s.af.refreshOnce()).resolves.toBe("new"); // следующая попытка — новый refresh
  });

  it("refresh, завершившийся после выхода, сессию не возвращает", async () => {
    const s = setup();
    const pending = s.call();
    await vi.waitFor(() => expect(s.refresh).toHaveBeenCalled());
    s.signOut();
    s.release();
    expect((await pending).status).toBe(401);
    expect(s.onRefreshed).not.toHaveBeenCalled();
  });

  it("ответ прежней сессии не повторяется от новой", async () => {
    const s = setup();
    let respond!: (r: Response) => void;
    const baseFetch = vi.fn(() => new Promise<Response>((r) => (respond = r)));
    let epoch = 0;
    const af = createAuthFetch({
      baseFetch,
      getToken: () => "other",
      getEpoch: () => epoch,
      refresh: s.refresh,
      onRefreshed: s.onRefreshed,
    });
    const pending = af.fetch("/api/v1/me", { headers: { Authorization: "Bearer old" } });
    epoch++; // выход и вход другим пользователем, пока запрос летел
    respond(new Response(null, { status: 401 }));
    expect((await pending).status).toBe(401);
    expect(baseFetch).toHaveBeenCalledTimes(1);
    expect(s.refresh).not.toHaveBeenCalled();
  });

  it("idle ждёт идущий refresh", async () => {
    const s = setup();
    const pending = s.call();
    await vi.waitFor(() => expect(s.refresh).toHaveBeenCalled());
    let done = false;
    const idle = s.af.idle().then(() => (done = true));
    await Promise.resolve();
    expect(done).toBe(false);
    s.release();
    await idle;
    await pending;
    expect(done).toBe(true);
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
