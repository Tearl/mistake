import { useRouter } from "@tanstack/react-router";
import { ChevronLeft } from "lucide-react";

// 二级页面（列表 / 详情 / 管理后台）顶部的返回条
export default function PageHeader({ title }: { title: string }) {
  const router = useRouter();
  return (
    <div className="sticky top-0 z-30 flex items-center gap-2 border-b border-[var(--c-border)] bg-white/90 px-3 py-3 backdrop-blur">
      <button
        type="button"
        onClick={() => router.history.back()}
        className="flex h-8 w-8 items-center justify-center rounded-[10px] text-[var(--c-dark)]"
        aria-label="返回"
      >
        <ChevronLeft size={22} />
      </button>
      <span className="h-serif text-[15px]">{title}</span>
    </div>
  );
}
