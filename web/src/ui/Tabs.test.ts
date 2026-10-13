import { describe, expect, it } from "vitest";

import { nextTab } from "./Tabs";

describe("nextTab", () => {
  it.each([
    [0, 3, "ArrowRight", 1],
    [2, 3, "ArrowRight", 0],
    [0, 3, "ArrowLeft", 2],
    [1, 3, "Home", 0],
    [0, 3, "End", 2],
    [1, 3, "Enter", null],
  ])("с %i из %i по %s → %s", (i, n, key, want) => expect(nextTab(i, n, key)).toBe(want));
});
