import { describe, expect, it } from "vitest";
import {
  filterFromParams,
  isSystemAdmin,
  listQuery,
  normalizeKeyInput,
  paramsForFilter,
  projectKeyError,
  visibleFilters,
} from "./logic";

describe("кнопка «Создать проект» и фильтр «Удалённые»", () => {
  it("видны администратору системы", () => {
    expect(isSystemAdmin({ is_system_admin: true })).toBe(true);
    expect(visibleFilters(true).map((f) => f.value)).toEqual(["active", "archived", "deleted"]);
  });

  it("не видны участнику и пока профиль не загружен", () => {
    expect(isSystemAdmin({ is_system_admin: false })).toBe(false);
    expect(isSystemAdmin({})).toBe(false);
    expect(isSystemAdmin(undefined)).toBe(false);
    expect(visibleFilters(false).map((f) => f.value)).toEqual(["active", "archived"]);
  });
});

describe("filterFromParams", () => {
  const p = (s: string) => new URLSearchParams(s);

  it("по адресу", () => {
    expect(filterFromParams(p(""), false)).toBe("active");
    expect(filterFromParams(p("archived=true"), false)).toBe("archived");
    expect(filterFromParams(p("deleted=true"), true)).toBe("deleted");
    expect(filterFromParams(p("archived=false"), true)).toBe("active");
  });

  it("«Удалённые» не администратору — обычный список", () => {
    expect(filterFromParams(p("deleted=true"), false)).toBe("active");
  });

  it("туда и обратно через адрес и запрос", () => {
    for (const f of ["active", "archived", "deleted"] as const) {
      expect(filterFromParams(new URLSearchParams(paramsForFilter(f)), true)).toBe(f);
    }
    expect(listQuery("active")).toEqual({});
    expect(listQuery("archived")).toEqual({ archived: true });
    expect(listQuery("deleted")).toEqual({ deleted: true });
  });
});

describe("ключ проекта", () => {
  it.each(["TM", "ABCDEFGHIJ", "TASK"])("%s подходит", (k) => {
    expect(projectKeyError(k)).toBe("");
  });

  it.each(["", "T", "ABCDEFGHIJK", "T1", "tm", "ТМ"])("%s не подходит", (k) => {
    expect(projectKeyError(k)).not.toBe("");
  });

  it("ввод приводится к заглавным без пробелов", () => {
    expect(normalizeKeyInput(" tm x")).toBe("TMX");
  });
});
