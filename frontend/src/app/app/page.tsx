"use client";

import { Messenger } from "@/features/messenger/Messenger";
import { Suspense } from "react";

export default function AppPage() {
  return (
    <Suspense>
      <Messenger />
    </Suspense>
  );
}
