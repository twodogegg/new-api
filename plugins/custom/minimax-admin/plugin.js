// MiniMax Admin (H3) —— 把 minimax-admin 服务接成 new-api 的视频上游。
//
// 上游契约（minimax-admin 的 /v1 兼容层）：
//   POST /v1/h3/assets   multipart 上传素材 → {id, type, size, expires_in, preview_url}
//   POST /v1/videos      创建 → {id, status, ...}
//   GET  /v1/videos/{id} 查询 → {id, status, progress, url}
//   GET  /v1/videos/{id}/content 下载（签名 URL，不需要 Bearer）
//
// 素材 ID（task_xxx）可以直接写进 images / reference_images 数组，上游会展开。

const ASSET_UPLOAD_ACTION = "asset_upload";
const MODEL_ALIASES = {
  "minimax-h3-flash": "h3",
  "minimax-h3-standard": "h3",
  "h3-pika": "h3",
  "h3-nsfw-opt": "h3-nsfw",
};
const RATIOS = ["adaptive", "21:9", "16:9", "4:3", "1:1", "3:4", "9:16"];
const RESOLUTION_TIERS = ["480P", "576P", "720P", "768P", "1080P"];

export const meta = {
  apiVersion: 1,
  key: "minimax-admin",
  name: "MiniMax Admin (H3)",
  icon: "text:MM",
  version: "1.0.0",
  author: { name: "twodogegg" },
  description: {
    en: "MiniMax H3 video generation on a self-hosted minimax-admin service, with asset upload and reference ids",
    zh: "通过自建的 minimax-admin 服务生成 MiniMax H3 视频，支持素材上传与素材 ID 引用",
  },
  models: ["h3", "h3-opt", "h3-nsfw", "h3-nsfw-opt"],
  fetchMode: "per_task",
  auth: "api_key",
  protocols: ["openai_video"],
  routes: [
    {
      method: "POST",
      path: "/v1/h3/assets",
      type: "submit",
      decode: "uploadAsset",
      render: "assetUploaded",
      retainResult: false,
    },
  ],
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    resolution: { enum: ["480P", "768P"], description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  },
  usageExamples: [
    { label: "480P", facts: { seconds: 5, resolution: "480P" } },
    { label: "768P", facts: { seconds: 5, resolution: "768P" } },
  ],
};

function object(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function root(ctx) {
  return ctx.baseUrl.replace(/\/+$/, "");
}

function modelFor(ctx) {
  const model = ctx.upstreamModel || ctx.model;
  return MODEL_ALIASES[model] || model;
}

function mediaList(value, name, limit) {
  if (value === undefined || value === null) return [];
  const list = Array.isArray(value) ? value : [value];
  if (list.length > limit) throw new Error(name + " accepts at most " + limit + " items");
  return list.filter(function (item) {
    return item !== undefined && item !== null && item !== "";
  });
}

/** 只保留上游认识的字段，避免把网关内部字段漏给 minimax-admin。 */
function canonicalRequest(ctx, body) {
  if (!object(body)) throw new Error("JSON object required");
  const model = modelFor(Object.assign({}, ctx || {}, { model: body.model || (ctx && ctx.model) }));
  const prompt = typeof body.prompt === "string" ? body.prompt : "";
  if (!prompt.trim()) throw new Error("prompt is required");
  if (body.seconds !== undefined && body.duration !== undefined && Number(body.seconds) !== Number(body.duration))
    throw new Error("seconds and duration must match");
  const seconds = body.seconds === undefined ? body.duration : body.seconds;
  const request = { model, prompt };
  if (seconds !== undefined) {
    if (!Number.isInteger(Number(seconds)) || Number(seconds) < 4 || Number(seconds) > 15)
      throw new Error("seconds must be an integer between 4 and 15");
    request.seconds = Number(seconds);
  }
  for (const field of ["size", "resolution"]) {
    if (body[field] !== undefined) {
      if (typeof body[field] !== "string" || !body[field].trim()) throw new Error(field + " must be a non-empty string");
      const value = body[field].trim();
      if (field === "resolution" && !RESOLUTION_TIERS.includes(value.toUpperCase()) && !/^\d+x\d+$/.test(value))
        throw new Error("resolution must be one of " + RESOLUTION_TIERS.join(", "));
      request[field] = value;
    }
  }
  if (body.ratio !== undefined && body.aspect_ratio !== undefined && body.ratio !== body.aspect_ratio)
    throw new Error("ratio and aspect_ratio must match");
  const ratio = body.aspect_ratio === undefined ? body.ratio : body.aspect_ratio;
  if (ratio !== undefined) {
    const value = ratio === "auto" ? "adaptive" : ratio;
    if (!RATIOS.includes(value)) throw new Error("ratio must be one of " + RATIOS.join(", "));
    request.ratio = value;
  }
  const images = mediaList(body.reference_images === undefined ? body.images : body.reference_images, "reference_images", 9);
  if (images.length) request.reference_images = images;
  const metadata = object(body.metadata) ? body.metadata : {};
  const videos = mediaList(
    body.reference_videos === undefined ? metadata.reference_video : body.reference_videos,
    "reference_videos",
    3,
  );
  if (videos.length) request.reference_videos = videos;
  const audios = mediaList(
    body.reference_audio === undefined ? metadata.reference_audio : body.reference_audio,
    "reference_audio",
    3,
  );
  if (audios.length) request.reference_audio = audios;
  if (body.enable_continuation !== undefined) request.enable_continuation = Boolean(body.enable_continuation);
  return request;
}

function videoUrl(task) {
  const data = object(task && task.data) ? task.data : {};
  if (typeof data.url === "string" && /^https?:\/\//.test(data.url)) return data.url;
  return "";
}

export function buildSubmitRequest(ctx) {
  if (ctx.action === ASSET_UPLOAD_ACTION) {
    if (!ctx.requestBody || typeof ctx.requestBody.fileRef !== "string" || !ctx.requestBody.fileRef)
      throw new Error("exactly one file is required");
    return {
      url: root(ctx) + "/v1/h3/assets",
      method: "POST",
      headers: { Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
      bodyType: "multipart",
      parts: [{ name: "file", fileRef: ctx.requestBody.fileRef, filename: ctx.requestBody.filename }],
    };
  }
  return {
    url: root(ctx) + "/v1/videos",
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + ctx.apiKey },
    body: canonicalRequest(ctx, ctx.requestBody),
  };
}

export function parseSubmitResponse(ctx, response) {
  const body = response && response.body;
  if (ctx.action === ASSET_UPLOAD_ACTION) {
    if (!object(body) || typeof body.id !== "string" || !body.id) throw new Error("素材上传失败：上游没有返回素材 ID");
    return { taskId: body.id, taskData: body, immediate: { status: "SUCCESS", progress: "100%" } };
  }
  if (!object(body)) throw new Error("MiniMax Admin 提交失败：响应不是对象");
  const taskId = body.id || body.task_id;
  if (typeof taskId !== "string" || !taskId) throw new Error("MiniMax Admin 提交失败：没有返回任务 ID");
  return { taskId, taskData: body };
}

export function buildQueryRequest(ctx) {
  return {
    url: root(ctx) + "/v1/videos/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Authorization: "Bearer " + ctx.apiKey },
  };
}

export function parseTaskResult(ctx, body) {
  const statuses = {
    queued: "QUEUED",
    pending: "QUEUED",
    in_progress: "IN_PROGRESS",
    running: "IN_PROGRESS",
    completed: "SUCCESS",
    succeeded: "SUCCESS",
    failed: "FAILURE",
    cancelled: "FAILURE",
  };
  const status = statuses[(body || {}).status] || "UNKNOWN";
  const result = { status };
  const progress = Number((body || {}).progress);
  if (Number.isFinite(progress) && progress >= 0 && progress <= 100) result.progress = String(progress) + "%";
  if (status === "SUCCESS") {
    const url = (body || {}).url;
    if (typeof url !== "string" || !/^https?:\/\//.test(url))
      throw new Error("MiniMax Admin 任务成功但没有返回可下载的视频地址");
    result.url = url;
  }
  if (status === "FAILURE") {
    const error = (body || {}).error;
    result.reason = typeof error === "string" ? error : object(error) && typeof error.message === "string" ? error.message : "MiniMax Admin 任务失败";
  }
  return result;
}

export function extractUsage(ctx) {
  if (ctx.action === ASSET_UPLOAD_ACTION) return { seconds: 0, resolution: "480P" };
  const body = ctx.requestBody || {};
  const seconds = Number(body.seconds === undefined ? body.duration : body.seconds);
  const raw = String((body.resolution || body.size || "480P")).toUpperCase();
  const resolution = /1080/.test(raw) ? "1080P" : /768|720|1344x/.test(raw) ? "768P" : "480P";
  return { seconds: Number.isFinite(seconds) && seconds > 0 ? seconds : 5, resolution };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" && videoUrl(task) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  const url = videoUrl(ctx);
  if (ctx.artifactKey !== "video" || !url) throw new Error("artifact_not_found");
  // 上游给的是带签名的直链，new-api 取片时不需要再带 API key
  return { url, method: ctx.clientRequest.method, credentialless: true };
}

export const native = {
  uploadAsset: function (ctx) {
    if (!ctx.body || ctx.body.kind !== "multipart") throw new Error("multipart body required");
    if (!Array.isArray(ctx.body.files) || ctx.body.files.length !== 1) throw new Error("exactly one file is required");
    const file = ctx.body.files[0];
    if (file.field !== "file") throw new Error("file field is required");
    return {
      kind: "submit",
      model: modelFor(ctx),
      action: ASSET_UPLOAD_ACTION,
      requestBody: { fileRef: file.ref, filename: file.filename, mimeType: file.mimeType, size: file.size },
    };
  },
  assetUploaded: function (_ctx, task) {
    const data = object(task && task.data) ? task.data : {};
    if (typeof data.id !== "string" || !data.id) throw new Error("MiniMax Admin 没有返回素材 ID");
    return {
      id: data.id,
      type: typeof data.type === "string" ? data.type : "",
      size: typeof data.size === "number" || typeof data.size === "string" ? data.size : "",
      expires_in: typeof data.expires_in === "number" ? data.expires_in : 86400,
      preview_url: typeof data.preview_url === "string" ? data.preview_url : "",
    };
  },
  error: function (_ctx, error) {
    const types = {
      400: "bad_request_error",
      401: "authorized_error",
      402: "insufficient_balance_error",
      403: "permission_error",
      404: "not_found_error",
      409: "conflict_error",
      413: "payload_too_large_error",
      422: "invalid_request_error",
      429: "rate_limit_error",
    };
    return {
      type: "error",
      error: { type: types[error.httpStatus] || "server_error", message: error.message, http_code: String(error.httpStatus) },
    };
  },
};

export const protocols = {
  openai_video: {
    decodeRequest(ctx) {
      if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
      const body = ctx.body.value;
      if (!object(body)) throw new Error("request body must be an object");
      if (typeof body.prompt !== "string" || !body.prompt.trim()) throw new Error("prompt is required");
      const request = canonicalRequest(ctx, body);
      const hasReference = request.reference_images || request.reference_videos || request.reference_audio;
      return { kind: "submit", model: ctx.model, action: hasReference ? "image_to_video" : "text_to_video", requestBody: request };
    },
    render(_ctx, task) {
      const output = { object: "video" };
      if (task.status === "SUCCESS") output.video_url = "/v1/videos/" + task.task_id + "/content";
      if (task.status === "FAILURE") output.error = { code: "video_generation_failed", message: task.fail_reason || "Video generation failed" };
      return output;
    },
  },
};
