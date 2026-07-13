import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { toast } from "sonner";

import { type Mistake, api, topicOf } from "@/lib/api";
import { MASTERY, subjectColor } from "@/lib/theme";

export const Route = createFileRoute("/review")({
  component: ReviewComponent,
});

interface Similar {
  question: string;
  answer: string;
  analysis: string;
  show: boolean;
  added: boolean;
}

function ReviewComponent() {
  const queryClient = useQueryClient();
  const [activeSubject, setActiveSubject] = useState("全部");
  const [onlyUnmastered, setOnlyUnmastered] = useState(false);
  const [current, setCurrent] = useState<Mistake | null>(null);
  const [poolCount, setPoolCount] = useState(0);
  const [reviewed, setReviewed] = useState(0);
  const [empty, setEmpty] = useState(false);
  const [showAnswer, setShowAnswer] = useState(false);
  const [similars, setSimilars] = useState<Similar[]>([]);
  const [similarLoading, setSimilarLoading] = useState(false);

  // 学科 chips 由统计接口的 bySubject 提供
  const statsQ = useQuery({ queryKey: ["stats"], queryFn: api.stats });
  const chips = ["全部", ...(statsQ.data?.bySubject.map((s) => s.subject) ?? [])];

  const draw = async () => {
    setShowAnswer(false);
    setSimilars([]);
    try {
      const { data, poolCount } = await api.random(
        activeSubject === "全部" ? "" : activeSubject,
        onlyUnmastered ? "unmastered" : "all",
      );
      setPoolCount(poolCount);
      if (data) {
        setCurrent(data);
        setEmpty(false);
        setReviewed((r) => r + 1);
      } else {
        setCurrent(null);
        setEmpty(true);
      }
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  // 初次加载 / 切换筛选时重新抽题
  useEffect(() => {
    setReviewed(0);
    draw();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeSubject, onlyUnmastered]);

  const grade = async (action: "unknown" | "fuzzy" | "mastered") => {
    if (!current) return;
    try {
      await api.grade(current._id, action);
      queryClient.invalidateQueries({ queryKey: ["stats"] });
      draw();
    } catch (err) {
      toast.error((err as Error).message);
    }
  };

  const genSimilar = async () => {
    if (!current || similars.length >= 3) return;
    setSimilarLoading(true);
    try {
      const items = await api.similar({
        subject: current.subject,
        knowledgePoints: current.knowledgePoints || [],
        questionType: current.questionType,
        difficulty: current.difficulty,
        ocrText: current.ocrText,
        answer: current.answer,
        count: 1,
        exclude: similars.map((s) => s.question),
      });
      const incoming = items.map((x) => ({ ...x, show: false, added: false }));
      setSimilars((s) => [...s, ...incoming]);
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setSimilarLoading(false);
    }
  };

  const cardColor = current ? subjectColor(current.subject) : "#E63946";

  return (
    <div className="px-4 pt-5">
      <div className="mb-5 flex items-center justify-between">
        <h1 className="h-serif text-[22px]">随机复习</h1>
        {poolCount > 0 && (
          <span className="rounded-[10px] bg-[rgba(230,57,70,0.1)] px-4 py-1.5 text-xs font-semibold text-[var(--c-primary)]">
            {reviewed} / {poolCount}
          </span>
        )}
      </div>

      {/* 筛选 chips */}
      <div className="mb-6 flex gap-3 overflow-x-auto whitespace-nowrap pb-1">
        {chips.map((c) => (
          <button
            type="button"
            key={c}
            onClick={() => setActiveSubject(c)}
            className={`shrink-0 rounded-full border px-6 py-2 text-xs ${
              activeSubject === c
                ? "border-[var(--c-primary)] bg-[var(--c-primary)] text-white"
                : "border-[rgba(230,57,70,0.2)] text-[var(--c-muted)]"
            }`}
          >
            {c}
          </button>
        ))}
        <button
          type="button"
          onClick={() => setOnlyUnmastered((v) => !v)}
          className={`shrink-0 rounded-full border px-6 py-2 text-xs ${
            onlyUnmastered
              ? "border-[#c79100] bg-[rgba(255,209,102,0.2)] text-[#c79100]"
              : "border-[rgba(230,57,70,0.2)] text-[var(--c-muted)]"
          }`}
        >
          未掌握
        </button>
      </div>

      {empty && (
        <div className="center">
          <div>这个范围还没有错题</div>
          <div className="text-xs opacity-70">换个筛选条件，或先去上传</div>
        </div>
      )}

      {current && (
        <>
          {/* 题目卡 */}
          <div className="overflow-hidden rounded-[16px] border-[3px] border-[rgba(26,26,46,0.08)] shadow-[0_6px_24px_rgba(0,0,0,0.05)]">
            <div className="flex items-center justify-between bg-[var(--c-dark)] px-5 py-4">
              <div className="flex min-w-0 items-center gap-4">
                <span
                  className="rounded-[8px] px-4 py-1.5 text-[11px] text-white"
                  style={{ background: cardColor }}
                >
                  {current.subject || "未分类"}
                </span>
                <span className="mono truncate text-[11px] text-white/60">{topicOf(current)}</span>
              </div>
              {current.difficulty && (
                <span className="flex-shrink-0 rounded-[8px] bg-[var(--c-yellow)] px-4 py-1 text-[11px] text-[var(--c-dark)]">
                  {current.difficulty}
                </span>
              )}
            </div>

            <div className="bg-white p-5">
              {current.imageFileID ? (
                <img
                  src={current.imageFileID}
                  alt="题目"
                  className="block w-full rounded-[12px] bg-[#f0f1f3]"
                />
              ) : (
                <p className="py-1 text-[15px] leading-loose text-[var(--c-dark)]">
                  {current.ocrText || "（无题干）"}
                </p>
              )}

              {showAnswer ? (
                <div className="mt-6 border-t-2 border-dashed border-[rgba(230,57,70,0.3)] pt-6">
                  <p className="mb-3 text-xs font-bold text-[var(--c-green)]">✓ 参考答案</p>
                  <p className="whitespace-pre-line text-[13px] leading-relaxed text-[var(--c-dark)]">
                    {current.answer || "（这道题没有保存答案）"}
                  </p>
                  {current.errorReason && (
                    <div className="mt-4 rounded-[12px] bg-[rgba(230,57,70,0.06)] px-5 py-4">
                      <span className="mr-3 text-[10px] font-bold text-[var(--c-primary)]">易错点</span>
                      <span className="text-xs text-[var(--c-muted)]">{current.errorReason}</span>
                    </div>
                  )}
                </div>
              ) : (
                <button
                  type="button"
                  onClick={() => setShowAnswer(true)}
                  className="mt-6 w-full rounded-[14px] border-[3px] py-3 text-sm font-semibold"
                  style={{ borderColor: cardColor, color: cardColor }}
                >
                  查看答案
                </button>
              )}
            </div>

            <div className="flex items-center gap-3 bg-[rgba(230,57,70,0.05)] px-5 py-3">
              <span className="font-extrabold text-[var(--c-primary)]">!</span>
              <span className="text-[11px] text-[var(--c-muted)]">
                已错 <span className="font-bold text-[var(--c-primary)]">{current.wrongCount || 1}</span> 次 ·
                当前{MASTERY[current.mastery].label}
              </span>
            </div>
          </div>

          {/* 三档反馈 */}
          <div className="mt-6 flex gap-4">
            <GradeButton main="不会" sub="再学" bg="rgba(230,57,70,0.1)" color="var(--c-primary)" onClick={() => grade("unknown")} />
            <GradeButton main="模糊" sub="多练" bg="rgba(255,209,102,0.18)" color="#c79100" onClick={() => grade("fuzzy")} />
            <GradeButton main="掌握" sub="✓" bg="rgba(6,214,160,0.12)" color="var(--c-green)" onClick={() => grade("mastered")} />
          </div>

          <button
            type="button"
            onClick={draw}
            className="mt-6 w-full rounded-[14px] bg-[var(--c-dark)] py-3.5 text-sm font-semibold text-white"
          >
            ⤮ 下一题
          </button>

          {/* 举一反三 */}
          <button
            type="button"
            disabled={similars.length >= 3 || similarLoading}
            onClick={genSimilar}
            className="mt-5 w-full rounded-[14px] border py-3.5 text-sm font-semibold disabled:opacity-50"
            style={
              similars.length >= 3
                ? { background: "#f0f1f3", color: "var(--c-muted)", borderColor: "var(--c-border)" }
                : { background: "rgba(17,138,178,0.1)", color: "var(--c-blue)", borderColor: "rgba(17,138,178,0.3)" }
            }
          >
            {similarLoading
              ? "AI 正在出题…"
              : similars.length >= 3
                ? "已出 3 题（上限）"
                : similars.length === 0
                  ? "✦ 举一反三 · AI 出一题"
                  : `再出一题（${similars.length}/3）`}
          </button>

          {similarLoading && (
            <div className="mt-5 rounded-[14px] border border-[var(--c-border)] bg-white p-5">
              {["90%", "70%", "40%"].map((w, i) => (
                <div
                  key={i}
                  className="mb-4 h-3 rounded-[8px] last:mb-0"
                  style={{
                    width: w,
                    backgroundImage: "linear-gradient(90deg,#edeef1 0%,#f7f8fa 50%,#edeef1 100%)",
                    backgroundSize: "200% 100%",
                    animation: "mistake-shimmer 1.2s ease-in-out infinite",
                  }}
                />
              ))}
            </div>
          )}

          {similars.length > 0 && (
            <div className="mt-5 flex flex-col gap-4">
              {similars.map((item, index) => (
                <div key={index} className="rounded-[14px] border border-[var(--c-border)] bg-white p-5">
                  <p className="text-[13px] leading-relaxed text-[var(--c-dark)]">
                    {index + 1}. {item.question}
                  </p>
                  {item.show && (
                    <div className="mt-4 border-t-2 border-dashed border-[rgba(17,138,178,0.3)] pt-4">
                      <div className="mb-2.5 flex">
                        <span className="w-[52px] flex-shrink-0 text-[11px] font-bold text-[var(--c-blue)]">答案</span>
                        <span className="flex-1 text-[12px] leading-relaxed text-[var(--c-muted)]">{item.answer}</span>
                      </div>
                      <div className="flex">
                        <span className="w-[52px] flex-shrink-0 text-[11px] font-bold text-[var(--c-blue)]">解析</span>
                        <span className="flex-1 text-[12px] leading-relaxed text-[var(--c-muted)]">{item.analysis}</span>
                      </div>
                    </div>
                  )}
                  <div className="mt-4 flex items-center justify-between">
                    <button
                      type="button"
                      className="text-xs text-[var(--c-blue)]"
                      onClick={() =>
                        setSimilars((s) => s.map((x, i) => (i === index ? { ...x, show: !x.show } : x)))
                      }
                    >
                      {item.show ? "收起" : "查看答案"}
                    </button>
                    <button
                      type="button"
                      className={`text-xs font-semibold ${item.added ? "text-[var(--c-green)]" : "text-[var(--c-primary)]"}`}
                      onClick={async () => {
                        if (item.added || !current) return;
                        try {
                          await api.createMistake({
                            imageFileID: "",
                            kind: "variant",
                            fromMistakeId: current._id,
                            subject: current.subject,
                            knowledgePoints: current.knowledgePoints,
                            questionType: current.questionType,
                            difficulty: current.difficulty,
                            ocrText: item.question,
                            answer: item.analysis ? `${item.answer}\n解析：${item.analysis}` : item.answer,
                            errorReason: "",
                            mastery: "unmastered",
                            wrongCount: 0,
                          });
                          queryClient.invalidateQueries();
                          setSimilars((s) => s.map((x, i) => (i === index ? { ...x, added: true } : x)));
                          toast.success("已加入错题库");
                        } catch (err) {
                          toast.error((err as Error).message);
                        }
                      }}
                    >
                      {item.added ? "✓ 已加入" : "＋ 加入错题库"}
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}

function GradeButton({
  main,
  sub,
  bg,
  color,
  onClick,
}: {
  main: string;
  sub: string;
  bg: string;
  color: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex flex-1 flex-col items-center gap-1 rounded-[14px] py-3.5"
      style={{ background: bg, color }}
    >
      <span className="text-sm font-bold">{main}</span>
      <span className="text-[10px] opacity-70">{sub}</span>
    </button>
  );
}
