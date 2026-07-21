import { useQuery } from "@tanstack/react-query";
import { Link, createFileRoute } from "@tanstack/react-router";

import { api } from "@/lib/api";
import { MASTERY, type Mastery, subjectColor } from "@/lib/theme";

export const Route = createFileRoute("/stats")({
  component: StatsComponent,
});

// 演示用：把统计页的「管理后台」入口默认展示（单用户模式下 dev 用户即 admin）
const IS_ADMIN = true;

const MAX_BAR = 96; // 柱状图最高 px

function StatsComponent() {
  const stats = useQuery({ queryKey: ["stats"], queryFn: api.stats }).data ?? {
    total: 0,
    mastered: 0,
    reviewing: 0,
    unmastered: 0,
    pending: 0,
    streak: 0,
    bySubject: [],
    weekly: [],
  };
  const total = stats.total;

  const maxWeek = Math.max(1, ...stats.weekly.map((w) => w.count));
  const weekly = stats.weekly.map((w) => ({
    ...w,
    h: Math.max(3, Math.round((w.count / maxWeek) * MAX_BAR)),
  }));

  const mastery = (["mastered", "reviewing", "unmastered"] as Mastery[]).map((key) => {
    const count = stats[key];
    return {
      label: MASTERY[key].label,
      color: MASTERY[key].color,
      count,
      pct: total ? Math.round((count / total) * 100) : 0,
    };
  });

  const maxSubj = Math.max(1, ...stats.bySubject.map((s) => s.count));
  const subjects = stats.bySubject.map((s) => ({
    ...s,
    color: subjectColor(s.subject),
    pct: Math.round((s.count / maxSubj) * 100),
  }));

  return (
    <div className="px-4 pt-5">
      <h1 className="h-serif mb-6 text-[22px]">学习统计</h1>

      {/* 本周柱状图 */}
      <Panel title="本周复习题数">
        <div className="flex h-[110px] items-end gap-3.5">
          {weekly.map((item, i) => (
            <div key={i} className="flex flex-1 flex-col items-center justify-end gap-2">
              <span
                className="mono text-[10px] font-bold"
                style={{ color: item.count > 0 ? "var(--c-primary)" : "#c0c4cc" }}
              >
                {item.count}
              </span>
              <div
                className="w-full rounded-t-[8px]"
                style={{ height: item.h, background: item.count > 0 ? "var(--c-primary)" : "#e6e7ea" }}
              />
              <span className="text-[9px] text-[var(--c-muted)]">{item.label}</span>
            </div>
          ))}
        </div>
      </Panel>

      {/* 掌握程度分布 */}
      <Panel title="掌握程度分布">
        {mastery.map((m) => (
          <div key={m.label} className="mb-5 last:mb-0">
            <div className="mb-2.5 flex justify-between">
              <span className="text-[13px] font-semibold">{m.label}</span>
              <span className="mono text-xs" style={{ color: m.color }}>
                {m.count} / {total}
              </span>
            </div>
            <div className="h-[7px] w-full overflow-hidden rounded-full bg-[#eceef1]">
              <div className="h-full rounded-full" style={{ width: `${m.pct}%`, background: m.color }} />
            </div>
          </div>
        ))}
      </Panel>

      {/* 科目错题排行 */}
      <Panel title="科目错题排行">
        {subjects.length === 0 ? (
          <p className="text-xs text-[var(--c-muted)]">暂无数据</p>
        ) : (
          subjects.map((s, index) => (
            <div key={s.subject} className="flex items-center gap-4" style={{ marginBottom: index === subjects.length - 1 ? 0 : 18 }}>
              <span
                className="flex h-[19px] w-[19px] flex-shrink-0 items-center justify-center rounded-[5px] text-[11px] font-bold text-white"
                style={{ background: index === 0 ? "var(--c-primary)" : "var(--c-dark)" }}
              >
                {index + 1}
              </span>
              <span className="flex-1 text-[13px] font-semibold">{s.subject || "未分类"}</span>
              <div className="h-1.5 w-[90px] overflow-hidden rounded-[4px] bg-[#eceef1]">
                <div className="h-full rounded-[4px]" style={{ width: `${s.pct}%`, background: s.color }} />
              </div>
              <span className="w-7 text-right text-xs font-bold" style={{ color: s.color }}>
                {s.count}
              </span>
            </div>
          ))
        )}
      </Panel>

      {/* 管理后台入口（仅管理员） */}
      {IS_ADMIN && (
        <Link
          to="/admin"
          className="mb-6 flex items-center rounded-[16px] border border-[var(--c-border)] bg-white px-5 py-5"
        >
          <span className="text-sm font-semibold">管理后台</span>
          <span className="ml-4 flex-1 text-[11px] text-[var(--c-muted)]">查看全部用户与错题</span>
          <span className="text-[18px] text-[var(--c-muted)]">›</span>
        </Link>
      )}

      {/* 性能面板入口（运维） */}
      {IS_ADMIN && (
        <Link
          to="/ops"
          className="mb-6 flex items-center rounded-[16px] border border-[var(--c-border)] bg-white px-5 py-5"
        >
          <span className="text-sm font-semibold">性能面板</span>
          <span className="ml-4 flex-1 text-[11px] text-[var(--c-muted)]">接口请求量 · 延迟 p95 · 错误率</span>
          <span className="text-[18px] text-[var(--c-muted)]">›</span>
        </Link>
      )}

      {/* 连续天数 */}
      <div className="flex items-center gap-6 rounded-[16px] bg-[var(--c-dark)] p-6">
        <span className="flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-[14px] bg-[var(--c-yellow)] text-[24px] text-[var(--c-dark)]">
          ★
        </span>
        <div>
          <span className="h-serif block text-[24px] text-white">{stats.streak}天</span>
          <span className="mt-1 block text-[13px] text-white/60">
            {stats.streak > 0 ? "连续学习中，继续保持！" : "今天开始复习，点亮第一天"}
          </span>
        </div>
      </div>
    </div>
  );
}

function Panel({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mb-6 rounded-[16px] border border-[var(--c-border)] bg-white p-6">
      <p className="mb-6 text-xs text-[var(--c-muted)]">{title}</p>
      {children}
    </div>
  );
}
