import { shouldRefresh } from "@/shared/api/transport";
import { describe, expect, it } from "vitest";

describe("shouldRefresh", () => {
  it("refreshes only expired sessions on protected calls", () => {
    expect(shouldRefresh("getMe", true)).toBe(true);
    expect(shouldRefresh("login", true)).toBe(false);
    expect(shouldRefresh("refresh", true)).toBe(false);
    expect(shouldRefresh("getMe", false)).toBe(false);
  });
});