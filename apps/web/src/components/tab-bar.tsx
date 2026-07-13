import { Link } from "@tanstack/react-router";
import { BarChart3, Home, Shuffle, Upload } from "lucide-react";

const TABS = [
  { to: "/", label: "首页", Icon: Home },
  { to: "/upload", label: "上传", Icon: Upload },
  { to: "/review", label: "复习", Icon: Shuffle },
  { to: "/stats", label: "统计", Icon: BarChart3 },
] as const;

export default function TabBar() {
  return (
    <nav className="fixed inset-x-0 bottom-0 z-50 mx-auto flex max-w-[448px] border-t border-[var(--c-border)] bg-white pb-[env(safe-area-inset-bottom)]">
      {TABS.map(({ to, label, Icon }) => (
        <Link
          key={to}
          to={to}
          activeOptions={{ exact: to === "/" }}
          className="flex flex-1 flex-col items-center gap-1 py-2.5 text-[var(--c-muted)] [&.active]:text-[var(--c-primary)]"
        >
          {({ isActive }) => (
            <>
              <Icon size={22} strokeWidth={isActive ? 2.4 : 1.8} />
              <span className="text-[10px] [.active_&]:font-semibold">{label}</span>
              <span
                className={`h-1 w-1 rounded-full ${isActive ? "bg-[var(--c-primary)]" : "bg-transparent"}`}
              />
            </>
          )}
        </Link>
      ))}
    </nav>
  );
}
