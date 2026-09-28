import "./globals.css";
import { Providers } from "@/shared/providers";
import type { Metadata } from "next";

export const metadata: Metadata = { title: "Rokko", description: "Мессенджер" };

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="ru" className="dark">
      <body>
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
