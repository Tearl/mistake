import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute, useRouter } from "@tanstack/react-router";
import { toast } from "sonner";

import PageHeader from "@/components/page-header";
import { api } from "@/lib/api";
import { MASTERY, MASTERY_KEYS, type Mastery, subjectColor } from "@/lib/theme";

export const Route = createFileRoute("/detail/$id")({
  component: DetailComponent,
});

function DetailComponent() {
  const { id } = Route.useParams();
  const router = useRouter();
  const queryClient = useQueryClient();
  const { data: item, isLoading } = useQuery({
    queryKey: ["mistake", id],
    queryFn: () => api.getMistake(id),
    retry: false,
  });

  if (!item) {
    return (
      <div>
        <PageHeader title="错题详情" />
        <div className="center">{isLoading ? "加载中…" : "未找到该错题"}</div>
      </div>
    );
  }

  const cardColor = subjectColor(item.subject);
  const knowledgeText = item.knowledgePoints.length ? item.knowledgePoints.join("，") : "—";

  const changeMastery = async (m: Mastery) => {
    if (m === item.mastery) return;
    try {
      await api.updateMastery(item._id, m);
      queryClient.invalidateQueries();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const remove = async () => {
    if (!window.confirm("确定删除这道错题吗？删除后不可恢复")) return;
    try {
      await api.deleteMistake(item._id);
      queryClient.invalidateQueries();
      router.history.back();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  return (
    <div>
      <PageHeader title="错题详情" />
      <div className="px-4 pb-10 pt-4">
        {/* 图片 / 文字题干 */}
        <div className="relative mb-6">
          <span
            className="absolute left-4 top-4 z-[2] rounded-[10px] px-5 py-1.5 text-[11px] text-white"
            style={{ background: cardColor }}
          >
            {item.subject || "未分类"}
          </span>
          {item.imageFileID ? (
            <img src={item.imageFileID} alt="错题" className="block w-full rounded-[16px] bg-[#f0f1f3]" />
          ) : (
            <div className="rounded-[16px] bg-[rgba(17,138,178,0.08)] px-6 pb-6 pt-14 text-[15px] leading-loose text-[var(--c-dark)]">
              {item.ocrText || "（无题干）"}
            </div>
          )}
        </div>

        {/* 信息表 */}
        <div className="rounded-[16px] border border-[var(--c-border)] bg-white px-6 py-1">
          <InfoLine k="知识点" v={knowledgeText} />
          <InfoLine k="题型" v={item.questionType || "—"} />
          <InfoLine k="难度" v={item.difficulty || "—"} />
          <InfoLine k="已错" v={`${item.wrongCount || 1} 次`} />
          {item.errorReason && <InfoLine k="错误原因" v={item.errorReason} column />}
          {item.imageFileID && item.ocrText && <InfoLine k="题干" v={item.ocrText} column />}
          {item.answer && <InfoLine k="参考答案" v={item.answer} column />}
        </div>

        {/* 掌握状态 */}
        <div className="mt-7">
          <p className="mb-4 text-xs text-[var(--c-muted)]">掌握状态</p>
          <div className="flex gap-4">
            {MASTERY_KEYS.map((m) => {
              const on = item.mastery === m;
              return (
                <button
                  type="button"
                  key={m}
                  onClick={() => changeMastery(m)}
                  className="flex-1 rounded-[14px] border py-3.5 text-center text-[13px] font-semibold"
                  style={
                    on
                      ? { background: MASTERY[m].color, color: "#fff", borderColor: MASTERY[m].color }
                      : { background: "#fff", color: "var(--c-muted)", borderColor: "var(--c-border)" }
                  }
                >
                  {MASTERY[m].label}
                </button>
              );
            })}
          </div>
        </div>

        <button
          type="button"
          onClick={remove}
          className="mt-9 w-full rounded-[14px] border border-[rgba(230,57,70,0.3)] bg-white py-3.5 text-sm font-semibold text-[var(--c-primary)]"
        >
          删除错题
        </button>
      </div>
    </div>
  );
}

function InfoLine({ k, v, column }: { k: string; v: string; column?: boolean }) {
  return (
    <div
      className={`border-b border-[rgba(26,26,46,0.06)] py-5 last:border-b-0 ${column ? "flex flex-col" : "flex"}`}
    >
      <span className="mono w-[112px] flex-shrink-0 text-[11px] text-[var(--c-muted)]">{k}</span>
      <span className={`flex-1 text-[13px] text-[var(--c-dark)] ${column ? "mt-3 whitespace-pre-line leading-relaxed" : ""}`}>
        {v}
      </span>
    </div>
  );
}
