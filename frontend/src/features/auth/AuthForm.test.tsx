import "@testing-library/jest-dom/vitest";
import { AuthForm } from "@/features/auth/AuthForm";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({ useRouter: () => ({ replace: vi.fn() }) }));
vi.mock("@/shared/api/transport", () => ({
  api: () => ({ auth: { login: vi.fn(), register: vi.fn() } }),
  errorText: (err: unknown) => String(err),
}));

describe("AuthForm", () => {
  it("renders the login form", () => {
    render(<AuthForm mode="login" />);
    expect(screen.getByPlaceholderText("Email")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Войти" })).toBeInTheDocument();
  });
});
