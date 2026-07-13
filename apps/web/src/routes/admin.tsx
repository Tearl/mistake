import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import PageHeader from "@/components/page-header";
import { api } from "@/lib/api";

export const Route = createFileRoute("/admin")({
  component: AdminComponent,
});

function AdminComponent() {
  const data = useQuery({ queryKey: ["admin"], queryFn: api.admin }).data ?? {
    userCount: 0,
    mistakeCount: 0,
    masteredCount: 0,
    users: [],
  };

  return (
    <div>
      <PageHeader title="管理后台" />
      <div className="px-4 pb-10 pt-4">
        <div className="mb-6 flex gap-3">
          <AdminStat value={data.userCount} label="用户数" color="var(--c-primary)" />
          <AdminStat value={data.mistakeCount} label="错题总数" color="var(--c-dark)" />
          <AdminStat value={data.masteredCount} label="已掌握" color="var(--c-green)" />
        </div>

        <div className="rounded-[16px] border border-[var(--c-border)] bg-white p-6">
          <p className="mb-6 text-xs text-[var(--c-muted)]">用户错题排行（Top 50）</p>
          {data.users.length === 0 ? (
            <p className="text-xs text-[var(--c-muted)]">暂无数据</p>
          ) : (
            data.users.map((u, index) => (
              <div
                key={u.openid}
                className="flex items-center gap-4 border-b border-[rgba(26,26,46,0.06)] py-4 last:border-b-0"
              >
                <span
                  className="flex h-[19px] w-[19px] flex-shrink-0 items-center justify-center rounded-[5px] text-[11px] font-bold text-white"
                  style={{ background: index === 0 ? "var(--c-primary)" : "var(--c-dark)" }}
                >
                  {index + 1}
                </span>
                <span className="mono flex-1 text-xs text-[var(--c-dark)]">…{u.short}</span>
                <span className="text-xs font-bold text-[var(--c-primary)]">{u.count} 题</span>
                <span className="w-[75px] text-right text-[11px] text-[var(--c-green)]">已掌握 {u.mastered}</span>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}

function AdminStat({ value, label, color }: { value: number; label: string; color: string }) {
  return (
    <div className="flex flex-1 flex-col items-center gap-1.5 rounded-[16px] border border-[var(--c-border)] bg-white py-5">
      <span className="h-serif text-[22px]" style={{ color }}>
        {value}
      </span>
      <span className="text-[11px] text-[var(--c-muted)]">{label}</span>
    </div>
  );
}
