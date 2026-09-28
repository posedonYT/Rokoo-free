import { sessionFromAuth } from "@/shared/auth/store";
import { describe, expect, it } from "vitest";

describe("sessionFromAuth", () => {
  it("copies token fields from the auth response", () => {
    const session = sessionFromAuth({
      userId: "1",
      username: "ada",
      accessToken: "access",
      refreshToken: "refresh",
    });
    expect(session.username).toBe("ada");
    expect(session.accessToken).toBe("access");
  });
});
