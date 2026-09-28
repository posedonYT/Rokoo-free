"use client";

import { Code, ConnectError, createClient, type Interceptor } from "@connectrpc/connect";
import { createGrpcWebTransport } from "@connectrpc/connect-web";
import { AuthService } from "@/shared/api/gen/auth/v1/auth_pb";
import { GuildService } from "@/shared/api/gen/guild/v1/guild_pb";
import { MediaService } from "@/shared/api/gen/media/v1/media_pb";
import { MessageService } from "@/shared/api/gen/message/v1/message_pb";
import { UserService } from "@/shared/api/gen/user/v1/user_pb";
import { sessionFromAuth, useAuth } from "@/shared/auth/store";

export const apiBase = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8080";
export const wsBase = process.env.NEXT_PUBLIC_WS_URL || "ws://localhost:8081/ws";
export const voiceBase = process.env.NEXT_PUBLIC_VOICE_URL || "ws://localhost:8082/ws";

export function shouldRefresh(method: string, unauthenticated: boolean) {
  if (!unauthenticated) return false;
  return method !== "refresh" && method !== "login" && method !== "register";
}

let refreshing: Promise<void> | null = null;

async function refreshSession() {
  const current = useAuth.getState().session;
  if (!current) throw new Error("no session");
  const bare = createGrpcWebTransport({ baseUrl: apiBase });
  const auth = createClient(AuthService, bare);
  const next = await auth.refresh({ refreshToken: current.refreshToken });
  useAuth.getState().setSession(sessionFromAuth(next));
}

const authInterceptor: Interceptor = (next) => async (req) => {
  const token = useAuth.getState().session?.accessToken;
  if (token) req.header.set("Authorization", `Bearer ${token}`);
  try {
    return await next(req);
  } catch (err) {
    const method = req.method.name;
    if (!(err instanceof ConnectError) || !shouldRefresh(method, err.code === Code.Unauthenticated)) {
      throw err;
    }
    refreshing ??= refreshSession().finally(() => {
      refreshing = null;
    });
    await refreshing;
    const updated = useAuth.getState().session?.accessToken;
    if (updated) req.header.set("Authorization", `Bearer ${updated}`);
    return await next(req);
  }
};

export function transport() {
  return createGrpcWebTransport({ baseUrl: apiBase, interceptors: [authInterceptor] });
}

export function api() {
  const t = transport();
  return {
    auth: createClient(AuthService, t),
    users: createClient(UserService, t),
    guilds: createClient(GuildService, t),
    messages: createClient(MessageService, t),
    media: createClient(MediaService, t),
  };
}

export function errorText(err: unknown) {
  if (err instanceof ConnectError) return err.rawMessage || err.message;
  if (err instanceof Error) return err.message;
  return "Неизвестная ошибка";
}
