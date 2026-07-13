import { useQuery } from "@tanstack/react-query";
import { Link, createFileRoute } from "@tanstack/react-router";

import { api, topicOf } from "@/lib/api";
import { MASTERY, subjectColor } from "@/lib/theme";

export const Route = createFileRoute("/")({
  component: HomeComponent,
});

function HomeComponent() {
  const statsQ = useQuery({ queryKey: ["stats"], queryFn: api.stats });
  const recentQ = useQuery({ queryKey: ["mistakes", { limit: 3 }], queryFn: () => api.listMistakes("", 3) });

  const stats = statsQ.data ?? {
    total: 0,
    mastered: 0,
    reviewing: 0,
    unmastered: 0,
    pending: 0,
    streak: 0,
    bySubject: [],
    weekly: [],
  };
  const subjects = stats.bySubject.map((s) => ({ ...s, color: subjectColor(s.subject) }));
  const recent = (recentQ.data ?? []).map((q) => ({
    ...q,
    color: subjectColor(q.subject),
    topic: topicOf(q),
  }));

  return (
    <div className="px-4 pt-4">
      {/* Hero banner */}
      <div className="relative overflow-hidden rounded-[20px] bg-[var(--c-dark)] p-6">
        <div className="absolute -right-4 -top-5 h-24 w-24 rounded-full bg-[var(--c-yellow)] opacity-15" />
        <div className="absolute inset-y-0 right-0 w-1.5 bg-[var(--c-yellow)]" />
        <div className="relative z-10">
          <div className="mono text-[11px] font-semibold text-[var(--c-yellow)]">TODAY'S PLAN</div>
          <div className="h-serif mt-3 text-[28px] leading-tight text-white">
            你有 <span className="text-[var(--c-primary)]">{stats.pending}</span> 道题
          </div>
          <div className="h-serif text-[28px] leading-tight text-white">待复习</div>
          <Link
            to="/review"
            className="mt-5 inline-block rounded-[10px] bg-[var(--c-primary)] px-6 py-3 text-[13px] font-semibold text-white"
          >
            开始复习 ›
          </Link>
        </div>
      </div>

      {/* Stat row */}
      <div className="mt-6 flex gap-3">
        <StatCard value={stats.total} label="总错题" color="var(--c-primary)" />
        <StatCard value={stats.mastered} label="已掌握" color="var(--c-green)" />
        <StatCard value={stats.streak} label="连续天数" color="var(--c-yellow)" />
      </div>

      {/* Subject grid */}
      <Section title="科目分类" moreLabel="全部">
        {subjects.length === 0 ? (
          <p className="py-4 text-[13px] text-[var(--c-muted)]">还没有错题，去「上传」加一道吧</p>
        ) : (
          <div className="flex flex-wrap gap-3">
            {subjects.map((s) => (
              <Link
                key={s.subject}
                to="/list"
                search={{ subject: s.subject }}
                className="relative box-border w-[calc(50%-6px)] overflow-hidden rounded-[16px] border p-5"
                style={{ background: `${s.color}18`, borderColor: `${s.color}30` }}
              >
                <span
                  className="absolute -right-3 -top-3 h-11 w-11 rounded-full opacity-20"
                  style={{ background: s.color }}
                />
                <span className="h-serif mb-2 block text-[24px]" style={{ color: s.color }}>
                  {s.subject[0]}
                </span>
                <span className="block text-sm font-semibold">{s.subject || "未分类"}</span>
                <span className="mt-1 block text-[11px] text-[var(--c-muted)]">
                  {s.count} 道错题
                </span>
              </Link>
            ))}
          </div>
        )}
      </Section>

      {/* Recent */}
      <Section title="最近错题" moreLabel="更多">
        {recent.length === 0 ? (
          <p className="py-4 text-[13px] text-[var(--c-muted)]">暂无记录</p>
        ) : (
          <div className="flex flex-col gap-3">
            {recent.map((item) => (
              <Link
                key={item._id}
                to="/detail/$id"
                params={{ id: item._id }}
                className="flex items-start gap-4 rounded-[14px] border border-[var(--c-border)] bg-white p-4"
              >
                <span
                  className="flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-[10px] text-[13px] font-bold text-white"
                  style={{ background: item.color }}
                >
                  {item.subject[0]}
                </span>
                <div className="min-w-0 flex-1">
                  <div className="mb-1 flex items-center gap-3">
                    <span className="text-[13px] font-semibold">{item.topic}</span>
                    {item.difficulty && (
                      <span
                        className="rounded-[6px] px-2 py-0.5 text-[10px]"
                        style={{ background: `${item.color}20`, color: item.color }}
                      >
                        {item.difficulty}
                      </span>
                    )}
                  </div>
                  <p className="truncate text-[11px] text-[var(--c-muted)]">
                    {item.ocrText || "（无题干文字）"}
                  </p>
                </div>
                <span
                  className="flex-shrink-0 rounded-full px-3 py-1 text-[10px]"
                  style={{
                    background: `${MASTERY[item.mastery].color}18`,
                    color: MASTERY[item.mastery].color,
                  }}
                >
                  {MASTERY[item.mastery].label}
                </span>
              </Link>
            ))}
          </div>
        )}
      </Section>
    </div>
  );
}

function StatCard({ value, label, color }: { value: number; label: string; color: string }) {
  return (
    <div className="flex flex-1 flex-col items-center gap-1.5 rounded-[16px] border border-[var(--c-border)] bg-white py-5">
      <span className="h-serif text-[26px]" style={{ color }}>
        {value}
      </span>
      <span className="text-[11px] text-[var(--c-muted)]">{label}</span>
    </div>
  );
}

function Section({
  title,
  moreLabel,
  children,
}: {
  title: string;
  moreLabel: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mt-8">
      <div className="mb-4 flex items-center justify-between">
        <h2 className="h-serif text-[17px]">{title}</h2>
        <Link to="/list" className="text-xs text-[var(--c-primary)]">
          {moreLabel}
        </Link>
      </div>
      {children}
    </section>
  );
}
