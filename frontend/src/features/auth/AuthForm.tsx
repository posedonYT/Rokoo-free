"use client";

import { errorText, api } from "@/shared/api/transport";
import { sessionFromAuth, useAuth } from "@/shared/auth/store";
import { Button } from "@/shared/ui/button";
import { Input } from "@/shared/ui/input";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";

export function AuthForm({ mode }: { mode: "login" | "register" }) {
  const router = useRouter();
  const setSession = useAuth((s) => s.setSession);
  const [email, setEmail] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setPending(true);
    setError("");
    try {
      const client = api().auth;
      const res = mode === "login" ? await client.login({ email, password }) : await client.register({ email, username, password });
      setSession(sessionFromAuth(res));
      router.replace("/app/");
    } catch (err) {
      setError(errorText(err));
    } finally {
      setPending(false);
    }
  }

  return (
    <main className="grid min-h-screen place-items-center bg-bg px-4">
      <form onSubmit={submit} className="w-full max-w-sm space-y-3 rounded-xl border border-line bg-panel p-6">
        <h1 className="text-xl font-semibold">{mode === "login" ? "Вход в Rokko" : "Создать аккаунт"}</h1>
        <Input placeholder="Email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        {mode === "register" && (
          <Input placeholder="Имя пользователя" value={username} onChange={(e) => setUsername(e.target.value)} required />
        )}
        <Input placeholder="Пароль" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        {error && <p className="text-sm text-red-400">{error}</p>}
        <Button className="w-full" disabled={pending} type="submit">
          {mode === "login" ? "Войти" : "Зарегистрироваться"}
        </Button>
        <p className="text-sm text-muted">
          {mode === "login" ? <Link href="/register/">Нет аккаунта? Регистрация</Link> : <Link href="/login/">Уже есть аккаунт? Войти</Link>}
        </p>
      </form>
    </main>
  );
}
