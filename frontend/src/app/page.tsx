"use client";

import { useAuth } from "@/shared/auth/store";
import { useRouter } from "next/navigation";
import { useEffect } from "react";

export default function Home() {
  const session = useAuth((s) => s.session);
  const router = useRouter();
  useEffect(() => {
    router.replace(session ? "/app/" : "/login/");
  }, [router, session]);
  return null;
}
