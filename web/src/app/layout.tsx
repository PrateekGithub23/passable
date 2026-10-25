// Root layout: wraps every page with <html>/<body>, global CSS and the font.

import type { Metadata, Viewport } from "next";
import { Atkinson_Hyperlegible_Next } from "next/font/google";
import "./globals.css";

// Atkinson Hyperlegible: designed by the Braille Institute for maximum legibility for
// low-vision readers. Self-hosted by next/font at build time (no request to Google at runtime).
const atkinson = Atkinson_Hyperlegible_Next({
  variable: "--font-atkinson",
  subsets: ["latin"],
  // next/font has no size-adjust metrics for this family, so name plain system fallbacks.
  fallback: ["system-ui", "Arial", "sans-serif"],
  adjustFontFallback: false,
});

export const metadata: Metadata = {
  title: "Passable: accessible routes around UW",
  description:
    "Step-free and winter-safe walking routes on the University of Waterloo campus and uptown Waterloo, updated live by the people using them.",
};

export const viewport: Viewport = {
  themeColor: "#0f766e",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={`${atkinson.variable} h-full antialiased`}>
      <body className="h-full">{children}</body>
    </html>
  );
}
