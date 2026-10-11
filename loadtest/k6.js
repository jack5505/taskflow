// Нагрузочный тест (ТЗ §10.3, §10.4): 50 одновременных пользователей на данных генератора
// (api/cmd/loadgen). Порог: 95% запросов доски, карточки и списка задач быстрее 300 мс, отчёт по
// времени за месяц быстрее 3 с. Запуск со стендом — loadtest/run.sh.
//
// Переменные: BASE_URL (по умолчанию http://api:8080), PASSWORD (пароль load<N>@example.com),
// VUS (50), DURATION (3m — длительность ровной нагрузки), REPORT_EVERY (5 — отчёт в каждой N-й итерации).
import http from "k6/http";
import { check, fail, group, sleep } from "k6";

const BASE = __ENV.BASE_URL || "http://api:8080";
const PASSWORD = __ENV.PASSWORD || "loadtest-password";
const VUS = Number(__ENV.VUS || 50);
const REPORT_EVERY = Number(__ENV.REPORT_EVERY || 5);
const BOARD_STATUSES = "todo,in_progress,review,done";

export const options = {
  scenarios: {
    users: {
      executor: "ramping-vus",
      startVUs: 0,
      stages: [
        { duration: "30s", target: VUS },
        { duration: __ENV.DURATION || "3m", target: VUS },
        { duration: "10s", target: 0 },
      ],
      gracefulRampDown: "10s",
    },
  },
  thresholds: {
    // kind — тег запроса: api — доска, карточка, список; report — отчёт за месяц.
    "http_req_duration{kind:api}": ["p(95)<300"],
    "http_req_duration{kind:report}": ["p(95)<3000"],
    // По каждому запросу отдельно — тот же порог, чтобы в итоге было видно, какой из них медленный.
    ...Object.fromEntries(
      ["sprints", "board", "list", "task", "worklogs", "comments", "history"].map((n) => [`http_req_duration{name:${n}}`, ["p(95)<300"]]),
    ),
    ...Object.fromEntries(["report_user", "report_task"].map((n) => [`http_req_duration{name:${n}}`, ["p(95)<3000"]])),
    http_req_failed: ["rate<0.01"],
    checks: ["rate>0.99"],
  },
  // Тела ответов k6 читает только там, где они нужны сценарию (спринты, доска, список): отчёт по
  // задачам за месяц у менеджера всех проектов — ~10 МБ JSON, и его разбор в k6 съедал бы CPU
  // того же сервера, на котором стоит стенд.
  discardResponseBodies: true,
  summaryTrendStats: ["avg", "min", "med", "p(90)", "p(95)", "p(99)", "max"],
};

// Пользователи генератора: load2 — менеджер всех проектов, load3 — исполнитель во всех, load4 —
// смешанная роль, load5… — участники трёх проектов. Вход один раз на пользователя (bcrypt дорогой и
// в сценарий §10.4 не входит); run.sh ставит JWT_ACCESS_TTL=30m — токена хватает на весь прогон.
export function setup() {
  const users = [];
  for (let n = 2; n < 2 + VUS; n++) {
    const res = http.post(`${BASE}/api/v1/auth/login`, JSON.stringify({ email: `load${n}@example.com`, password: PASSWORD }), {
      headers: { "Content-Type": "application/json" },
      tags: { kind: "setup" },
      responseType: "text",
    });
    if (res.status !== 200) fail(`вход load${n}: ${res.status} ${res.body}`);
    const token = res.json("access_token");
    const projects = http.get(`${BASE}/api/v1/projects`, { headers: auth(token), tags: { kind: "setup" }, responseType: "text" });
    const keys = projects.json().map((p) => p.key);
    if (!keys.length) fail(`у load${n} нет проектов — данные не от генератора?`);
    users.push({ token, keys });
  }
  return { users, month: previousMonth() };
}

export default function (data) {
  const u = data.users[(__VU - 1) % data.users.length];
  const key = u.keys[Math.floor(Math.random() * u.keys.length)];
  const params = (name, kind = "api") => ({ headers: auth(u.token), tags: { name, kind } });
  const withBody = (name) => ({ ...params(name), responseType: "text" });
  let seen = [];

  group("доска", () => {
    const sprints = http.get(`${BASE}/api/v1/projects/${key}/sprints`, withBody("sprints"));
    check(sprints, { "спринты 200": (r) => r.status === 200 });
    const active = sprints.status === 200 && sprints.json().find((s) => s.state === "active");
    if (!active) return;
    const board = http.get(
      `${BASE}/api/v1/projects/${key}/tasks?status=${BOARD_STATUSES}&sprint_id=${active.id}&limit=200`,
      withBody("board"),
    );
    // Ответ разбирается один раз: JSON на 200 задач — заметная работа для самого k6.
    const items = board.status === 200 ? board.json("items") : [];
    check(board, { "доска 200": (r) => r.status === 200 && items.length > 0 });
    seen = seen.concat(items);
  });
  sleep(1);

  group("список задач", () => {
    const page = http.get(`${BASE}/api/v1/projects/${key}/tasks?limit=50`, withBody("list"));
    check(page, { "список 200": (r) => r.status === 200 });
    if (page.status !== 200) return;
    const body = page.json();
    seen = seen.concat(body.items);
    const next = body.next_cursor;
    if (next) {
      const more = http.get(`${BASE}/api/v1/projects/${key}/tasks?limit=50&cursor=${encodeURIComponent(next)}`, params("list"));
      check(more, { "список, страница 2 200": (r) => r.status === 200 });
    }
  });
  sleep(1);

  // Карточка: то, что открывает страница задачи — задача, записи времени, комментарии, история.
  if (seen.length) {
    const t = seen[Math.floor(Math.random() * seen.length)].key;
    group("карточка", () => {
      const res = http.batch([
        ["GET", `${BASE}/api/v1/tasks/${t}`, null, params("task")],
        ["GET", `${BASE}/api/v1/tasks/${t}/worklogs`, null, params("worklogs")],
        ["GET", `${BASE}/api/v1/tasks/${t}/comments`, null, params("comments")],
        ["GET", `${BASE}/api/v1/tasks/${t}/history`, null, params("history")],
      ]);
      check(res[0], { "карточка 200": (r) => r.status === 200 });
      check(res.slice(1), { "вкладки карточки 200": (rs) => rs.every((r) => r.status === 200) });
    });
    sleep(1);
  }

  // Отчёт за прошлый месяц открывают реже, чем доску: каждая REPORT_EVERY-я итерация пользователя,
  // по очереди по пользователям и по задачам, по всем своим проектам или по одному.
  if ((__ITER + __VU) % REPORT_EVERY === 0) {
    group("отчёт за месяц", () => {
      const view = __ITER % 2 === 0 ? "user" : "task";
      const scope = __VU % 2 === 0 ? `&project=${key}` : "";
      const res = http.get(
        `${BASE}/api/v1/reports/time?from=${data.month.from}&to=${data.month.to}&group_by=${view}${scope}`,
        params(`report_${view}`, "report"),
      );
      check(res, { "отчёт 200": (r) => r.status === 200 });
    });
    sleep(1);
  }
}

function auth(token) {
  return { Authorization: `Bearer ${token}` };
}

// Прошлый календарный месяц: в нём записи генератора есть за каждый день.
function previousMonth() {
  const now = new Date();
  const from = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - 1, 1));
  const to = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 0));
  const day = (d) => d.toISOString().slice(0, 10);
  return { from: day(from), to: day(to) };
}
