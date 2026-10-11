import { describe, expect, it } from "vitest";
import type { MeProfile, ProjectListItem } from "../api/client";
import {
  affectedByCreate,
  apiFieldErrors,
  canManageProject,
  creatableProjects,
  emptyForm,
  hasOpenDialog,
  isCreateHotkey,
  isTypingTarget,
  openSprints,
  projectKeyFromPath,
  validateCreate,
  type CreateForm,
  type HotkeyEvent,
} from "./create";

const form = (f: Partial<CreateForm> = {}): CreateForm => ({ ...emptyForm("TM"), title: "Починить вход", ...f });

describe("validateCreate", () => {
  it("достаточно проекта и названия: умолчания §3.3, пустые поля не отправляются", () => {
    const { body, errors } = validateCreate(form({ title: "  Починить вход  " }), true);
    expect(errors).toEqual({});
    expect(body).toEqual({ title: "Починить вход", type: "task", priority: "medium" });
  });

  it("проект и название обязательны", () => {
    const { errors } = validateCreate(form({ projectKey: "", title: "   " }), false);
    expect(errors).toEqual({ project: "Выберите проект", title: "Название — от 1 до 200 символов" });
  });

  it("название до 200 символов, считая символы, а не байты", () => {
    expect(validateCreate(form({ title: "я".repeat(200) }), false).errors).toEqual({});
    expect(validateCreate(form({ title: "я".repeat(201) }), false).errors.title).toBeTruthy();
  });

  it("все поля менеджера уходят в запрос", () => {
    const { body, errors } = validateCreate(
      form({
        type: "bug",
        priority: "critical",
        description: "**шаги**",
        assigneeId: "u1",
        sprintId: "s1",
        estimate: "2h 30m",
        dueDate: "2026-10-31",
        labels: "web, auth, web",
      }),
      true,
    );
    expect(errors).toEqual({});
    expect(body).toEqual({
      title: "Починить вход",
      type: "bug",
      priority: "critical",
      description: "**шаги**",
      assignee_id: "u1",
      sprint_id: "s1",
      estimate_minutes: 150,
      due_date: "2026-10-31",
      labels: ["web", "auth"],
    });
  });

  it("исполнитель и спринт не уходят без прав менеджера (§2): сервер ответил бы 403", () => {
    const { body } = validateCreate(form({ assigneeId: "u1", sprintId: "s1" }), false);
    expect(body.assignee_id).toBeUndefined();
    expect(body.sprint_id).toBeUndefined();
  });

  it("оценка в формате 2h 30m, от 1m до 1000h", () => {
    expect(validateCreate(form({ estimate: "полчаса" }), false).errors.estimate).toBe("Формат: 2h 30m, 45m, 3h, 1d");
    expect(validateCreate(form({ estimate: "1001h" }), false).errors.estimate).toBe("От 1m до 1000h");
    expect(validateCreate(form({ estimate: "45m" }), false).body.estimate_minutes).toBe(45);
  });

  it("метки: до 10 штук по 30 символов", () => {
    const eleven = Array.from({ length: 11 }, (_, i) => `l${i}`).join(",");
    expect(validateCreate(form({ labels: eleven }), false).errors.labels).toBe("Не больше 10 меток");
    expect(validateCreate(form({ labels: "x".repeat(31) }), false).errors.labels).toBe("Метка — до 30 символов");
  });

  it("описание до 20 000 символов", () => {
    expect(validateCreate(form({ description: "a".repeat(20001) }), false).errors.description).toBeTruthy();
  });
});

describe("apiFieldErrors", () => {
  it("ошибки сервера — к полям формы, путь labels[3] — к меткам", () => {
    expect(
      apiFieldErrors(
        [
          { field: "title", reason: "от 1 до 200 символов" },
          { field: "labels[3]", reason: "не длиннее 200" },
          { field: "sprint_id", reason: "спринт завершён" },
        ],
        true,
      ),
    ).toEqual({ title: "от 1 до 200 символов", labels: "не длиннее 200", sprint: "спринт завершён" });
  });

  it("ошибка по скрытому или неизвестному полю — в общую", () => {
    expect(
      apiFieldErrors(
        [
          { field: "assignee_id", reason: "не активный участник проекта" },
          { field: "foo", reason: "что-то" },
        ],
        false,
      ),
    ).toEqual({ general: "assignee_id: не активный участник проекта; foo: что-то" });
  });
});

describe("права и списки", () => {
  const me = (m: Partial<MeProfile>): MeProfile => ({ id: "u", ...m });
  const p = (x: Partial<ProjectListItem>): ProjectListItem => ({ key: "TM", name: "Задачи", ...x });

  it("исполнитель не видит полей исполнителя и спринта, менеджер и администратор — видят", () => {
    expect(canManageProject(me({}), p({ my_role: "member" }))).toBe(false);
    expect(canManageProject(me({}), p({ my_role: "manager" }))).toBe(true);
    expect(canManageProject(me({ is_system_admin: true }), p({}))).toBe(true);
    expect(canManageProject(undefined, p({ my_role: "manager" }))).toBe(false);
    expect(canManageProject(me({}), undefined)).toBe(false);
  });

  it("задачу создают только в действующем проекте", () => {
    const list = creatableProjects([
      p({ key: "ZZ" }),
      p({ key: "AR", is_archived: true }),
      p({ key: "DL", deleted_at: "2026-10-01T00:00:00Z" }),
      p({ key: "AA" }),
    ]);
    expect(list.map((x) => x.key)).toEqual(["AA", "ZZ"]);
  });

  it("завершённый спринт выбрать нельзя", () => {
    const list = openSprints([
      { id: "1", state: "active" },
      { id: "2", state: "planned" },
      { id: "3", state: "closed" },
    ]);
    expect(list.map((s) => s.id)).toEqual(["1", "2"]);
  });
});

describe("projectKeyFromPath", () => {
  it("страницы проекта: доска, бэклог, карточка задачи", () => {
    expect(projectKeyFromPath("/projects/TM")).toBe("TM");
    expect(projectKeyFromPath("/projects/TM/backlog")).toBe("TM");
    expect(projectKeyFromPath("/tasks/TM-42")).toBe("TM");
  });

  it("вне проекта клавиша не работает", () => {
    expect(projectKeyFromPath("/")).toBeNull();
    expect(projectKeyFromPath("/login")).toBeNull();
    expect(projectKeyFromPath("/tasks/TM")).toBeNull();
  });
});

describe("горячая клавиша C", () => {
  const ev = (e: Partial<HotkeyEvent>): HotkeyEvent => ({
    key: "c",
    code: "KeyC",
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    shiftKey: false,
    repeat: false,
    defaultPrevented: false,
    ...e,
  });

  it("C в любой раскладке", () => {
    expect(isCreateHotkey(ev({}))).toBe(true);
    expect(isCreateHotkey(ev({ key: "с" }))).toBe(true); // русская раскладка
    expect(isCreateHotkey(ev({ key: "C", code: "" }))).toBe(true); // Caps Lock
  });

  it("не срабатывает с модификаторами, на повторе и в наборе IME", () => {
    expect(isCreateHotkey(ev({ ctrlKey: true }))).toBe(false); // копирование
    expect(isCreateHotkey(ev({ metaKey: true }))).toBe(false);
    expect(isCreateHotkey(ev({ altKey: true }))).toBe(false);
    expect(isCreateHotkey(ev({ shiftKey: true }))).toBe(false);
    expect(isCreateHotkey(ev({ repeat: true }))).toBe(false);
    expect(isCreateHotkey(ev({ isComposing: true }))).toBe(false);
    expect(isCreateHotkey(ev({ defaultPrevented: true }))).toBe(false);
    expect(isCreateHotkey(ev({ key: "v", code: "KeyV" }))).toBe(false);
  });

  it("не срабатывает в полях ввода", () => {
    const el = (tagName: string, isContentEditable = false) => ({ tagName, isContentEditable }) as unknown as EventTarget;
    expect(isTypingTarget(el("INPUT"))).toBe(true);
    expect(isTypingTarget(el("TEXTAREA"))).toBe(true);
    expect(isTypingTarget(el("SELECT"))).toBe(true);
    expect(isTypingTarget(el("DIV", true))).toBe(true);
    expect(isTypingTarget(el("BODY"))).toBe(false);
    expect(isTypingTarget(el("BUTTON"))).toBe(false);
    expect(isTypingTarget(null)).toBe(false);
    expect(isTypingTarget({} as EventTarget)).toBe(false); // window, document
  });

  it("не срабатывает при открытом диалоге", () => {
    const doc = (found: boolean) => ({ querySelector: () => (found ? ({} as Element) : null) }) as Pick<Document, "querySelector">;
    expect(hasOpenDialog(doc(true))).toBe(true);
    expect(hasOpenDialog(doc(false))).toBe(false);
  });
});

describe("affectedByCreate", () => {
  it("устаревают запросы проекта (доска, бэклог) и сводки главной, чужие проекты — нет", () => {
    expect(affectedByCreate(["board", "TM", { sprint_id: "s" }], "TM")).toBe(true);
    expect(affectedByCreate(["backlog", "TM"], "TM")).toBe(true);
    expect(affectedByCreate(["project-sprints", "TM"], "TM")).toBe(true);
    expect(affectedByCreate(["projects", "active"], "TM")).toBe(true);
    expect(affectedByCreate(["my-tasks"], "TM")).toBe(true);
    expect(affectedByCreate(["board", "XX"], "TM")).toBe(false);
    expect(affectedByCreate(["task", "TM-1"], "TM")).toBe(false);
  });
});
