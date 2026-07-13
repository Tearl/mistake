import { useQuery } from "@tanstack/react-query";
import { Link, createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { toast } from "sonner";

import PageHeader from "@/components/page-header";
import { api } from "@/lib/api";
import { MASTERY, subjectColor } from "@/lib/theme";

export const Route = createFileRoute("/list")({
  validateSearch: (search: Record<string, unknown>): { subject?: string } => ({
    subject: typeof search.subject === "string" ? search.subject : undefined,
  }),
  component: ListComponent,
});

function ListComponent() {
  const { subject = "" } = Route.useSearch();
  const listQ = useQuery({
    queryKey: ["mistakes", { subject }],
    queryFn: () => api.listMistakes(subject),
  });
  const mistakes = (listQ.data ?? []).map((m) => ({ ...m, color: subjectColor(m.subject) }));

  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const selectedCount = Object.keys(selected).length;

  const toggle = (id: string) => {
    setSelected((s) => {
      const next = { ...s };
      if (next[id]) delete next[id];
      else next[id] = true;
      return next;
    });
  };

  const toggleAll = () => {
    if (selectedCount === mistakes.length) setSelected({});
    else setSelected(Object.fromEntries(mistakes.map((m) => [m._id, true])));
  };

  const cancelSelect = () => {
    setSelecting(false);
    setSelected({});
  };

  const exportSelected = async () => {
    if (!selectedCount) {
      toast.error("请先选择错题");
      return;
    }
    try {
      await api.exportDocx(subject, Object.keys(selected));
      toast.success(`已导出所选 ${selectedCount} 题`);
      cancelSelect();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  return (
    <div className={selecting ? "pb-24" : ""}>
      <PageHeader title={subject ? `${subject} · 错题` : "全部错题"} />

      <div className="px-4 pt-4">
        {mistakes.length > 0 && (
          <div className="mb-6 flex items-center justify-between">
            {selecting ? (
              <>
                <button type="button" onClick={toggleAll} className="text-[13px] font-semibold text-[var(--c-primary)]">
                  {selectedCount === mistakes.length ? "取消全选" : "全选"}
                </button>
                <button
                  type="button"
                  onClick={cancelSelect}
                  className="rounded-[10px] border border-[var(--c-border)] bg-white px-6 py-2 text-[13px] font-semibold text-[var(--c-muted)]"
                >
                  取消
                </button>
              </>
            ) : (
              <>
                <span className="text-[13px] text-[var(--c-muted)]">共 {mistakes.length} 题</span>
                <button
                  type="button"
                  onClick={() => setSelecting(true)}
                  className="rounded-[10px] bg-[var(--c-dark)] px-6 py-2 text-[13px] font-semibold text-white"
                >
                  选择导出
                </button>
              </>
            )}
          </div>
        )}

        {mistakes.length === 0 ? (
          <div className="center">
            <div>这里还没有错题</div>
            <div className="text-xs opacity-70">去「上传」加一道吧</div>
          </div>
        ) : (
          <div className="flex flex-wrap gap-3">
            {mistakes.map((item) => {
              const inner = (
                <>
                  {selecting && (
                    <span
                      className={`absolute right-3.5 top-3.5 z-[3] flex h-[21px] w-[21px] items-center justify-center rounded-full border text-[13px] text-white ${
                        selected[item._id]
                          ? "border-[var(--c-primary)] bg-[var(--c-primary)]"
                          : "border-[var(--c-border)] bg-white/90"
                      }`}
                    >
                      {selected[item._id] ? "✓" : ""}
                    </span>
                  )}
                  {item.imageFileID ? (
                    <img src={item.imageFileID} alt="缩略图" className="block h-[140px] w-full bg-[#f0f1f3] object-cover" />
                  ) : (
                    <div className="line-clamp-6 box-border h-[140px] overflow-hidden bg-[rgba(17,138,178,0.08)] p-4 text-[11px] leading-relaxed text-[var(--c-dark)]">
                      {item.ocrText || "变式题"}
                    </div>
                  )}
                  <div className="flex items-center justify-between px-4 py-3">
                    <span
                      className="rounded-[10px] px-3 py-0.5 text-[11px]"
                      style={{ background: `${item.color}18`, color: item.color }}
                    >
                      {item.subject || "未分类"}
                    </span>
                    <span
                      className="h-2.5 w-2.5 rounded-full"
                      style={{ background: MASTERY[item.mastery].color }}
                    />
                  </div>
                </>
              );
              const cls = `relative box-border w-[calc(50%-6px)] overflow-hidden rounded-[14px] border bg-white ${
                selecting && selected[item._id] ? "border-[var(--c-primary)]" : "border-[var(--c-border)]"
              }`;
              return selecting ? (
                <button type="button" key={item._id} onClick={() => toggle(item._id)} className={`${cls} text-left`}>
                  {inner}
                </button>
              ) : (
                <Link key={item._id} to="/detail/$id" params={{ id: item._id }} className={cls}>
                  {inner}
                </Link>
              );
            })}
          </div>
        )}
      </div>

      {selecting && (
        <div className="fixed inset-x-0 bottom-0 z-40 mx-auto flex max-w-[448px] items-center justify-between border-t border-[var(--c-border)] bg-white px-4 py-4">
          <span className="text-[13px] text-[var(--c-muted)]">已选 {selectedCount} 题</span>
          <button
            type="button"
            onClick={exportSelected}
            className="rounded-[10px] bg-[var(--c-dark)] px-10 py-3 text-sm font-semibold text-white disabled:opacity-50"
            disabled={!selectedCount}
          >
            导出所选
          </button>
        </div>
      )}
    </div>
  );
}
