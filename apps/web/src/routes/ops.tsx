import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";

import { api, type OpsRouteStat } from "@/lib/api";

export const Route = createFileRoute("/ops")({
  component: OpsComponent,
});

const MAX_BAR = 88; // 时序柱最高 px

function fmtTime(ms?: number): string {
  if (!ms) return "—";
  return new Date(ms).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

// 延迟着色：越慢越红
function latColor(ms: number): string {
  if (ms >= 1000) return "#e5484d";
  if (ms >= 300) return "var(--c-yellow)";
  return "var(--c-primary)";
}

function OpsComponent() {
  const { data, isLoading } = useQuery({
    queryKey: ["ops-summary"],
    queryFn: api.opsSummary,
    refetchInterval: 30_000, // 30 秒自动刷新
  });

  const summary = data;
  const available = summary && summary.available !== false && (summary.totalRequests ?? 0) >= 0 && summary.routes;

  const timeline = summary?.timeline ?? [];
  const maxBucket = Math.max(1, ...timeline.map((b) => b.count));
  const routes = [...(summary?.routes ?? [])];
  const maxP99 = Math.max(1, ...routes.map((r) => r.p99));

  return (
    <div className="px-4 pt-5">
      <div className="mb-6 flex items-baseline justify-between">
        <h1 className="h-serif text-[22px]">性能面板 /ops</h1>
        <span className="mono text-[10px] text-[var(--c-muted)]">
          {summary?.generatedAt ? `更新于 ${fmtTime(summary.generatedAt)}` : ""}
        </span>
      </div>

      {isLoading ? (
        <Panel title="加载中">
          <p className="text-xs text-[var(--c-muted)]">正在拉取聚合数据…</p>
        </Panel>
      ) : !summary || summary.available === false || (summary.totalRequests ?? 0) === 0 ? (
        <Panel title="暂无数据">
          <p className="text-xs text-[var(--c-muted)]">
            清洗任务尚未产出，或所选窗口内无请求。先访问几个接口，再等清洗任务跑一轮。
          </p>
        </Panel>
      ) : (
        <>
          {/* KPI 三连 */}
          <div className="mb-6 grid grid-cols-3 gap-3">
            <Kpi label="请求数" value={String(summary.totalRequests ?? 0)} />
            <Kpi
              label="错误率"
              value={`${(((summary.errorRate ?? 0) * 100) || 0).toFixed(1)}%`}
              danger={(summary.errorRate ?? 0) > 0}
            />
            <Kpi label="整体 p95" value={`${summary.overallP95 ?? 0}ms`} />
          </div>

          {/* 请求量时序 */}
          <Panel title={`请求量（每分钟 · 窗口 ${fmtTime(summary.windowStart)}–${fmtTime(summary.windowEnd)}）`}>
            {timeline.length === 0 ? (
              <p className="text-xs text-[var(--c-muted)]">窗口内无数据</p>
            ) : (
              <div className="flex h-[104px] items-end gap-1.5 overflow-x-auto">
                {timeline.map((b) => (
                  <div
                    key={b.minute}
                    className="flex flex-col items-center justify-end gap-1.5"
                    style={{ width: 16, flex: "0 0 auto" }}
                  >
                    <div
                      className="w-full rounded-t-[5px]"
                      style={{
                        height: Math.max(3, Math.round((b.count / maxBucket) * MAX_BAR)),
                        background: b.errors > 0 ? "#e5484d" : "var(--c-primary)",
                      }}
                      title={`${new Date(b.minute).toLocaleTimeString()} · ${b.count} 请求 · ${b.errors} 错误`}
                    />
                  </div>
                ))}
              </div>
            )}
          </Panel>

          {/* 各路由延迟与错误 */}
          <Panel title="接口性能（按请求量排序）">
            <div className="flex flex-col gap-4">
              {routes.map((r) => (
                <RouteRow key={r.route} r={r} maxP99={maxP99} />
              ))}
            </div>
          </Panel>
        </>
      )}
    </div>
  );
}

function Kpi({ label, value, danger }: { label: string; value: string; danger?: boolean }) {
  return (
    <div className="rounded-[14px] border border-[var(--c-border)] bg-white p-4">
      <p className="text-[10px] text-[var(--c-muted)]">{label}</p>
      <p className="mono mt-1 text-[20px] font-bold" style={{ color: danger ? "#e5484d" : "var(--c-dark)" }}>
        {value}
      </p>
    </div>
  );
}

function RouteRow({ r, maxP99 }: { r: OpsRouteStat; maxP99: number }) {
  return (
    <div>
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <span className="mono truncate text-[12px] font-semibold">{r.route}</span>
        <span className="mono flex-shrink-0 text-[10px] text-[var(--c-muted)]">
          {r.count} 次
          {r.errors > 0 && <span className="ml-2" style={{ color: "#e5484d" }}>{r.errors} 错误</span>}
        </span>
      </div>
      <div className="h-1.5 w-full overflow-hidden rounded-[4px] bg-[#eceef1]">
        <div
          className="h-full rounded-[4px]"
          style={{ width: `${Math.round((r.p99 / maxP99) * 100)}%`, background: latColor(r.p99) }}
        />
      </div>
      <div className="mono mt-1 flex gap-3 text-[10px] text-[var(--c-muted)]">
        <span>p50 {r.p50}ms</span>
        <span>p90 {r.p90}ms</span>
        <span style={{ color: latColor(r.p99) }}>p99 {r.p99}ms</span>
        <span>max {r.max}ms</span>
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
