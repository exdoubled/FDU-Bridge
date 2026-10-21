using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.IO.Pipes;
using System.Linq;
using System.Text;
using System.Threading.Tasks;
using System.Web.Script.Serialization;

namespace FduNodePicker
{
    internal sealed class PipeApi
    {
        private const int Limit = 4 * 1024 * 1024;
        private readonly string pipeName;
        private readonly int timeout;

        internal PipeApi(string pipeName, int timeoutMilliseconds)
        {
            this.pipeName = pipeName;
            timeout = timeoutMilliseconds;
        }

        internal static PipeApi Discover()
        {
            var names = Directory.GetFiles(@"\\.\pipe\")
                .Select(Path.GetFileName)
                .Where(n => n.StartsWith("verge-mihomo-production-", StringComparison.Ordinal)).ToArray();
            if (names.Length != 1)
                throw new InvalidOperationException(names.Length == 0
                    ? "未找到正在运行的 Clash Verge 控制接口。请确认 Clash 已打开，再点刷新。"
                    : "发现多个 Clash 控制接口，无法确定当前实例；未修改任何设置。");
            return new PipeApi(names[0], 5000);
        }

        internal T Request<T>(string method, string path, object payload)
        {
            if (!((method == "GET" && (path == "/configs" || path == "/proxies")) ||
                  (method == "PUT" && path == "/proxies/INTERNET")))
                throw new InvalidOperationException("小窗口只允许读取状态和切换 INTERNET。");
            var json = new JavaScriptSerializer { MaxJsonLength = Limit };
            byte[] body = payload == null ? new byte[0] : Encoding.UTF8.GetBytes(json.Serialize(payload));
            byte[] header = Encoding.ASCII.GetBytes(method + " " + path + " HTTP/1.1\r\nHost: localhost\r\n" +
                "Content-Type: application/json\r\nContent-Length: " + body.Length + "\r\nConnection: close\r\n\r\n");
            using (var pipe = new NamedPipeClientStream(".", pipeName, PipeDirection.InOut, PipeOptions.Asynchronous))
            {
                pipe.Connect(timeout);
                var exchange = Exchange(pipe, header, body);
                if (Task.WhenAny(exchange, Task.Delay(timeout)).GetAwaiter().GetResult() != exchange)
                {
                    pipe.Dispose();
                    exchange.ContinueWith(t => { var ignored = t.Exception; }, TaskContinuationOptions.OnlyOnFaulted);
                    throw new TimeoutException("Clash 控制接口响应超时，请稍后刷新。");
                }
                byte[] bytes = ReadResponse(exchange.GetAwaiter().GetResult());
                return bytes.Length == 0 ? default(T) : json.Deserialize<T>(Encoding.UTF8.GetString(bytes));
            }
        }

        private static async Task<byte[]> Exchange(Stream pipe, byte[] header, byte[] body)
        {
            await pipe.WriteAsync(header, 0, header.Length).ConfigureAwait(false);
            await pipe.WriteAsync(body, 0, body.Length).ConfigureAwait(false);
            using (var result = new MemoryStream())
            {
                var buffer = new byte[8192];
                int count;
                while ((count = await pipe.ReadAsync(buffer, 0, buffer.Length).ConfigureAwait(false)) != 0)
                {
                    if (result.Length + count > Limit) throw new InvalidOperationException("Clash 返回的数据过大，已停止读取。");
                    result.Write(buffer, 0, count);
                }
                return result.ToArray();
            }
        }

        private static string ReadLine(byte[] bytes, ref int offset)
        {
            for (int end = offset; end + 1 < bytes.Length; end++)
            {
                if (bytes[end] != 13 || bytes[end + 1] != 10) continue;
                string line = Encoding.ASCII.GetString(bytes, offset, end - offset);
                offset = end + 2;
                return line;
            }
            throw new InvalidOperationException("Clash 控制接口返回了不完整的数据，请刷新。");
        }

        private static byte[] ReadResponse(byte[] bytes)
        {
            int offset = 0, status;
            string[] first = ReadLine(bytes, ref offset).Split(' ');
            if (first.Length < 2 || !first[0].StartsWith("HTTP/1.") || !Int32.TryParse(first[1], out status))
                throw new InvalidOperationException("Clash 控制接口返回格式异常。");
            if (status < 200 || status >= 300)
                throw new InvalidOperationException("Clash 控制接口拒绝请求（HTTP " + status + "）。");
            var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            string line;
            while ((line = ReadLine(bytes, ref offset)).Length != 0)
            {
                int colon = line.IndexOf(':');
                if (colon <= 0) throw new InvalidOperationException("Clash HTTP 响应头格式异常。");
                headers[line.Substring(0, colon)] = line.Substring(colon + 1).Trim();
            }
            string transfer;
            if (headers.TryGetValue("Transfer-Encoding", out transfer) && transfer.Equals("chunked", StringComparison.OrdinalIgnoreCase))
            {
                using (var decoded = new MemoryStream())
                {
                    while (true)
                    {
                        int size;
                        string length = ReadLine(bytes, ref offset).Split(';')[0];
                        if (!Int32.TryParse(length, NumberStyles.HexNumber, CultureInfo.InvariantCulture, out size) || size < 0 || size > bytes.Length - offset - 2)
                            throw new InvalidOperationException("Clash 分块响应不完整。");
                        if (size == 0) return decoded.ToArray();
                        decoded.Write(bytes, offset, size);
                        offset += size;
                        if (bytes[offset++] != 13 || bytes[offset++] != 10)
                            throw new InvalidOperationException("Clash 分块响应格式异常。");
                    }
                }
            }
            string contentLength;
            int expected;
            if (headers.TryGetValue("Content-Length", out contentLength) &&
                (!Int32.TryParse(contentLength, out expected) || expected != bytes.Length - offset))
                throw new InvalidOperationException("Clash 响应长度不完整。");
            return bytes.Skip(offset).ToArray();
        }
    }

    internal sealed class ProxyData
    {
        public string type { get; set; }
        public string now { get; set; }
        public string[] all { get; set; }
    }
    internal sealed class ProxyResponse
    {
        public Dictionary<string, ProxyData> proxies { get; set; }
    }
    internal sealed class PickerState
    {
        internal string Mode, Global, Current, Problem;
        internal string[] Choices;
    }

    internal sealed class PickerClient
    {
        private readonly PipeApi explicitApi;
        internal PickerClient() { }
        internal PickerClient(PipeApi api) { explicitApi = api; }

        internal PickerState Load()
        {
            return Load(explicitApi ?? PipeApi.Discover());
        }

        private static PickerState Load(PipeApi api)
        {
            var config = api.Request<Dictionary<string, object>>("GET", "/configs", null);
            var response = api.Request<ProxyResponse>("GET", "/proxies", null);
            if (config == null || response == null || response.proxies == null)
                throw new InvalidOperationException("未能读取 Clash 状态，请刷新。");
            ProxyData global, internet;
            response.proxies.TryGetValue("GLOBAL", out global);
            response.proxies.TryGetValue("INTERNET", out internet);
            object mode;
            var state = new PickerState
            {
                Mode = config.TryGetValue("mode", out mode) ? Convert.ToString(mode) : "未知",
                Global = global == null ? "未找到" : global.now,
                Current = internet == null ? "未找到" : internet.now,
                Choices = internet == null || internet.all == null ? new string[0] : internet.all
                    .Where(n => SafeTarget(response.proxies, n, new HashSet<string>(StringComparer.Ordinal))).Distinct().ToArray()
            };
            if (!String.Equals(state.Mode, "global", StringComparison.OrdinalIgnoreCase))
                state.Problem = "当前不是全局模式。请在 Clash 中选择全局模式后刷新。";
            else if (state.Global != "FDU-ROUTER")
                state.Problem = "GLOBAL 当前未选中 FDU-ROUTER。请在 Clash 中选择后刷新。";
            else if (internet == null || internet.type != "Selector")
                state.Problem = "未找到可手动选择的 INTERNET 组。请检查是否启用了配套分流配置。";
            else if (state.Choices.Length == 0)
                state.Problem = "INTERNET 中没有可用的外网节点。";
            return state;
        }

        private static bool SafeTarget(Dictionary<string, ProxyData> proxies, string name, HashSet<string> visiting)
        {
            if (String.IsNullOrEmpty(name) || name == "GLOBAL" || name == "INTERNET" || name == "FDU" || name == "FDU-ROUTER") return false;
            ProxyData node;
            if (!proxies.TryGetValue(name, out node) || node == null || String.IsNullOrEmpty(node.type)) return false;
            if (node.type == "Direct" || node.type == "Reject" || node.type == "RejectDrop" || node.type == "Pass" || node.type == "Compatible") return false;
            if (!visiting.Add(name)) return false;
            try
            {
                return node.all == null || (node.all.Length > 0 && node.all.All(child => SafeTarget(proxies, child, visiting)));
            }
            finally { visiting.Remove(name); }
        }

        internal PickerState Select(string name)
        {
            // Re-read immediately before the sole permitted write, never repair mode or GLOBAL.
            var api = explicitApi ?? PipeApi.Discover();
            var before = Load(api);
            if (before.Problem != null) throw new InvalidOperationException(before.Problem);
            if (!before.Choices.Contains(name)) throw new InvalidOperationException("所选节点已不存在或可能形成回环，请刷新列表后重新选择。");
            if (before.Current == name) return before;
            try
            {
                api.Request<object>("PUT", "/proxies/INTERNET", new { name = name });
                var after = Load(api);
                if (after.Problem != null || after.Current != name)
                    throw new InvalidOperationException("读取的选择与请求不一致。");
                return after;
            }
            catch (Exception error)
            {
                throw new InvalidOperationException("切换请求已尝试发送，但未能确认结果。请点击刷新查看当前节点；小窗口不会自动重试。\r\n" + error.Message, error);
            }
        }
    }
}
