import * as http from "http";

// --- Types ---

export interface BelayHealthResponse {
  status: string;
  version: string;
  uptime: string;
}

export interface BelayEvent {
  event_id: string;
  timestamp: string;
  timestamp_nano: number;
  file_path: string;
  operation: "CREATE" | "MODIFY" | "DELETE" | "RENAME";
  content_hash?: string;
  previous_hash?: string;
  content_size: number;
  old_path?: string;
  session_id?: string;
  attribution_method: string;
  attribution_confidence: number;
  metadata?: Record<string, string>;
  is_conflict?: boolean;
}

export interface BelaySession {
  session_id: string;
  tool_name: string;
  pid: number;
  status: "active" | "ended" | "crashed";
  started_at: string;
  ended_at?: string;
  duration: string;
  label?: string;
  metadata?: Record<string, string>;
  files_changed: number;
  event_count: number;
}

interface EventsResponse {
  events: BelayEvent[];
  count: number;
}

interface SessionsResponse {
  sessions: BelaySession[];
  count: number;
}

interface FileHistoryResponse {
  path: string;
  events: BelayEvent[];
  count: number;
}

interface SessionEventsResponse {
  session_id: string;
  events: BelayEvent[];
  count: number;
}

// --- Client ---

export class BelayClient {
  private baseUrl: string;

  constructor(port: number = 33412) {
    this.baseUrl = `http://127.0.0.1:${port}`;
  }

  /**
   * Check if the Belay daemon is running and healthy.
   */
  async getHealth(): Promise<BelayHealthResponse> {
    return this.get<BelayHealthResponse>("/api/health");
  }

  /**
   * Get recent file change events.
   */
  async getEvents(limit: number = 50): Promise<BelayEvent[]> {
    const resp = await this.get<EventsResponse>(
      `/api/events?limit=${limit}`
    );
    return resp.events ?? [];
  }

  /**
   * Get sessions list.
   */
  async getSessions(limit: number = 20): Promise<BelaySession[]> {
    const resp = await this.get<SessionsResponse>(
      `/api/sessions?limit=${limit}&hide_empty=true`
    );
    return resp.sessions ?? [];
  }

  /**
   * Get change history for a specific file.
   */
  async getFileHistory(
    path: string,
    limit: number = 50
  ): Promise<BelayEvent[]> {
    const encoded = encodeURIComponent(path);
    const resp = await this.get<FileHistoryResponse>(
      `/api/files/history?path=${encoded}&limit=${limit}`
    );
    return resp.events ?? [];
  }

  /**
   * Get file content by its content hash.
   */
  async getFileContent(hash: string): Promise<string> {
    return this.getRaw(`/api/files/content?hash=${hash}`);
  }

  /**
   * Get events for a specific session.
   */
  async getSessionEvents(
    sessionId: string,
    limit: number = 100
  ): Promise<BelayEvent[]> {
    const resp = await this.get<SessionEventsResponse>(
      `/api/sessions/${sessionId}/events?limit=${limit}`
    );
    return resp.events ?? [];
  }

  // --- HTTP helpers ---

  private get<T>(path: string): Promise<T> {
    return new Promise((resolve, reject) => {
      const url = `${this.baseUrl}${path}`;
      http
        .get(url, { timeout: 5000 }, (res) => {
          let data = "";
          res.on("data", (chunk) => (data += chunk));
          res.on("end", () => {
            if (
              res.statusCode &&
              res.statusCode >= 200 &&
              res.statusCode < 300
            ) {
              try {
                resolve(JSON.parse(data));
              } catch (err) {
                reject(new Error(`Failed to parse response: ${err}`));
              }
            } else {
              reject(
                new Error(
                  `HTTP ${res.statusCode}: ${data.slice(0, 200)}`
                )
              );
            }
          });
        })
        .on("error", reject)
        .on("timeout", function () {
          this.destroy();
          reject(new Error("Request timed out"));
        });
    });
  }

  private getRaw(path: string): Promise<string> {
    return new Promise((resolve, reject) => {
      const url = `${this.baseUrl}${path}`;
      http
        .get(url, { timeout: 5000 }, (res) => {
          let data = "";
          res.on("data", (chunk) => (data += chunk));
          res.on("end", () => {
            if (
              res.statusCode &&
              res.statusCode >= 200 &&
              res.statusCode < 300
            ) {
              resolve(data);
            } else {
              reject(
                new Error(
                  `HTTP ${res.statusCode}: ${data.slice(0, 200)}`
                )
              );
            }
          });
        })
        .on("error", reject)
        .on("timeout", function () {
          this.destroy();
          reject(new Error("Request timed out"));
        });
    });
  }
}
