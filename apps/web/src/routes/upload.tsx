import { useQueryClient } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { useRef, useState } from "react";
import { toast } from "sonner";

import { api } from "@/lib/api";
import { DIFFICULTIES, SUBJECTS } from "@/lib/theme";

export const Route = createFileRoute("/upload")({
  component: UploadComponent,
});

type Phase = "idle" | "uploading" | "recognizing" | "recognized";

interface Form {
  subject: string;
  knowledgePoints: string;
  questionType: string;
  difficulty: string;
  ocrText: string;
  answer: string;
  errorReason: string;
}

const EMPTY_FORM: Form = {
  subject: "",
  knowledgePoints: "",
  questionType: "",
  difficulty: "中",
  ocrText: "",
  answer: "",
  errorReason: "",
};

function UploadComponent() {
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<Phase>("idle");
  const [imagePath, setImagePath] = useState(""); // 本地预览 URL
  const [imageFileID, setImageFileID] = useState(""); // 上传后服务器返回的图片地址
  const [form, setForm] = useState<Form>(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const fileRef = useRef<HTMLInputElement>(null);

  const onFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setImagePath(URL.createObjectURL(file));
    setForm(EMPTY_FORM);
    setPhase("uploading");
    try {
      const { imageFileID } = await api.upload(file);
      setImageFileID(imageFileID);
      setPhase("recognizing");
      // 调真实通义千问识别
      try {
        const d = await api.recognize(imageFileID);
        setForm({
          subject: d.subject || "",
          knowledgePoints: (d.knowledgePoints || []).join("，"),
          questionType: d.questionType || "",
          difficulty: d.difficulty || "中",
          ocrText: d.ocrText || "",
          answer: d.answer || "",
          errorReason: d.errorReason || "",
        });
      } catch (err) {
        // 识别失败（如未配置 AI key）：保留图片，转为手动填写
        toast.error(`${(err as Error).message}，请手动填写`);
      }
      setPhase("recognized");
    } catch (err) {
      toast.error((err as Error).message);
      reset();
    }
  };

  const reset = () => {
    setImagePath("");
    setImageFileID("");
    setForm(EMPTY_FORM);
    setSaving(false);
    setPhase("idle");
    if (fileRef.current) fileRef.current.value = "";
  };

  const save = async () => {
    if (!form.subject) {
      toast.error("请选择学科");
      return;
    }
    setSaving(true);
    try {
      await api.createMistake({
        imageFileID,
        subject: form.subject,
        knowledgePoints: form.knowledgePoints
          ? form.knowledgePoints.split(/[，,、\s]+/).filter(Boolean)
          : [],
        questionType: form.questionType,
        difficulty: form.difficulty,
        ocrText: form.ocrText,
        answer: form.answer,
        errorReason: form.errorReason,
        mastery: "unmastered",
        wrongCount: 1,
      });
      queryClient.invalidateQueries();
      toast.success("已保存");
      reset();
    } catch (err) {
      setSaving(false);
      toast.error((err as Error).message);
    }
  };

  const set = (field: keyof Form) => (v: string) => setForm((f) => ({ ...f, [field]: v }));

  return (
    <div className="px-4 pt-5">
      <h1 className="h-serif text-[22px]">上传错题</h1>
      <p className="mb-6 mt-1 text-[13px] text-[var(--c-muted)]">拍照或选取图片，AI 自动识别与归类</p>

      <input
        ref={fileRef}
        type="file"
        accept="image/*"
        className="hidden"
        onChange={onFile}
      />

      {/* Dropzone */}
      {phase === "idle" && (
        <button
          type="button"
          onClick={() => fileRef.current?.click()}
          className="flex w-full flex-col items-center gap-4 rounded-[20px] border-[3px] border-dashed border-[var(--c-primary)] bg-[rgba(230,57,70,0.03)] px-8 py-12"
        >
          <span className="flex h-14 w-14 items-center justify-center rounded-[18px] bg-[var(--c-primary)] text-[26px] text-white">
            ↑
          </span>
          <span className="text-sm font-semibold">点击上传错题图片</span>
          <span className="text-[11px] text-[var(--c-muted)]">支持拍照 / 相册，单张</span>
          <span className="mt-1 rounded-[10px] bg-[var(--c-primary)] px-8 py-3 text-[13px] font-semibold text-white">
            ＋ 选择图片
          </span>
        </button>
      )}

      {phase === "uploading" && (
        <p className="py-6 text-center text-[var(--c-primary)]">图片上传中…</p>
      )}

      {/* AI 识别动画 */}
      {phase === "recognizing" && (
        <div className="flex flex-col items-center gap-6 rounded-[20px] bg-[var(--c-dark)] px-8 py-14">
          <div
            className="relative flex h-14 w-14 items-center justify-center rounded-full bg-[var(--c-primary)] text-[24px]"
            style={{ animation: "mistake-pulse 1.2s ease-in-out infinite" }}
          >
            🧠
            <span className="absolute -right-1 -top-1 flex h-5 w-5 items-center justify-center rounded-full bg-[var(--c-yellow)] text-[11px]">
              ⚡
            </span>
          </div>
          <span className="h-serif text-[16px] text-white">AI 正在识别中…</span>
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-white/10">
            <div
              className="h-full rounded-full bg-[var(--c-yellow)]"
              style={{ animation: "mistake-slide 1.4s ease-in-out infinite" }}
            />
          </div>
          <span className="text-[11px] text-white/60">正在提取题目内容与知识点</span>
        </div>
      )}

      {/* 识别结果 / 确认表单 */}
      {phase === "recognized" && (
        <div className="rounded-[20px] border-[3px] border-[var(--c-green)] bg-[rgba(6,214,160,0.04)] p-5">
          <div className="mb-5 flex items-center gap-3">
            <span className="flex h-8 w-8 items-center justify-center rounded-full bg-[var(--c-green)] text-xs text-white">
              ✓
            </span>
            <span className="text-[13px] font-semibold text-[var(--c-green)]">
              识别完成，确认或修改后保存
            </span>
          </div>

          {imagePath && (
            <img
              src={imagePath}
              alt="错题"
              className="mb-5 block w-full rounded-[12px] bg-[var(--c-dark)]"
            />
          )}

          <PickerField label="科目" value={form.subject || "请选择"} options={SUBJECTS} onChange={set("subject")} placeholder={!form.subject} />
          <InputField label="知识点" value={form.knowledgePoints} onChange={set("knowledgePoints")} placeholder="逗号分隔，可多个" />
          <InputField label="题型" value={form.questionType} onChange={set("questionType")} placeholder="如 选择题 / 解答题" />
          <PickerField label="难度" value={form.difficulty} options={DIFFICULTIES} onChange={set("difficulty")} />
          <InputField label="错误原因" value={form.errorReason} onChange={set("errorReason")} placeholder="易错点 / 错因" />
          <TextAreaField label="题干" value={form.ocrText} onChange={set("ocrText")} placeholder="AI 识别的题目文字" />
          <TextAreaField label="参考答案" value={form.answer} onChange={set("answer")} placeholder="复习时点「查看答案」会显示" />

          <div className="mt-7 flex gap-4">
            <button
              type="button"
              disabled={saving}
              onClick={save}
              className="flex-1 rounded-[14px] bg-[var(--c-green)] py-3.5 text-sm font-semibold text-white disabled:opacity-60"
            >
              保存错题
            </button>
            <button
              type="button"
              onClick={reset}
              className="flex-1 rounded-[14px] border border-[var(--c-border)] bg-white py-3.5 text-sm font-semibold text-[var(--c-dark)]"
            >
              继续上传
            </button>
          </div>
        </div>
      )}

      {/* 拍照技巧 */}
      {(phase === "idle" || phase === "uploading") && (
        <div className="mt-8 rounded-[14px] bg-[rgba(255,209,102,0.16)] p-5">
          <p className="mb-3 text-xs font-bold text-[#b8860b]">拍照技巧</p>
          {["保持画面清晰，光线充足", "完整包含题目与答案区域", "避免手抖或遮挡关键部分"].map((t) => (
            <p key={t} className="mt-1.5 text-xs text-[var(--c-muted)]">
              · {t}
            </p>
          ))}
        </div>
      )}
    </div>
  );
}

function FieldRow({ label, children, column }: { label: string; children: React.ReactNode; column?: boolean }) {
  return (
    <div
      className={`border-b border-[rgba(26,26,46,0.06)] py-4 ${column ? "flex flex-col items-stretch" : "flex items-center"}`}
    >
      <span className="mono w-[100px] flex-shrink-0 text-[11px] text-[var(--c-muted)]">{label}</span>
      {children}
    </div>
  );
}

function InputField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <FieldRow label={label}>
      <input
        className="flex-1 bg-transparent text-[13px] outline-none placeholder:text-[#b8bcc4]"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
      />
    </FieldRow>
  );
}

function TextAreaField({
  label,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
}) {
  return (
    <FieldRow label={label} column>
      <textarea
        className="mt-3 min-h-[46px] w-full resize-none bg-transparent text-[13px] leading-relaxed outline-none placeholder:text-[#b8bcc4]"
        value={value}
        placeholder={placeholder}
        rows={2}
        onChange={(e) => onChange(e.target.value)}
      />
    </FieldRow>
  );
}

function PickerField({
  label,
  value,
  options,
  onChange,
  placeholder,
}: {
  label: string;
  value: string;
  options: string[];
  onChange: (v: string) => void;
  placeholder?: boolean;
}) {
  return (
    <FieldRow label={label}>
      <select
        className="flex-1 bg-transparent text-[13px] outline-none"
        style={placeholder ? { color: "#b8bcc4" } : undefined}
        value={placeholder ? "" : value}
        onChange={(e) => onChange(e.target.value)}
      >
        {placeholder && (
          <option value="" disabled>
            请选择
          </option>
        )}
        {options.map((o) => (
          <option key={o} value={o}>
            {o}
          </option>
        ))}
      </select>
    </FieldRow>
  );
}
