using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;

namespace FduNodePicker
{
    internal sealed class RuleDocument
    {
        public int version { get; set; }
        public string[] domains { get; set; }
        public string[] domain_suffixes { get; set; }
        public string[] ip_cidrs { get; set; }
        public DisabledRuleDocument disabled { get; set; }
        internal static RuleDocument Empty()
        {
            return new RuleDocument { version = 2, domains = new string[0], domain_suffixes = new string[0], ip_cidrs = new string[0], disabled = DisabledRuleDocument.Empty() };
        }
    }
    internal sealed class DisabledRuleDocument
    {
        public string[] domains { get; set; }
        public string[] domain_suffixes { get; set; }
        public string[] ip_cidrs { get; set; }
        internal static DisabledRuleDocument Empty()
        {
            return new DisabledRuleDocument { domains = new string[0], domain_suffixes = new string[0], ip_cidrs = new string[0] };
        }
    }
    internal sealed class RuleSnapshot
    {
        internal RuleDocument Document;
        internal string Hash;
    }
    internal sealed class RulesApplyState
    {
        internal bool Applied;
        internal string Message;
    }
    internal sealed class RuleStatus
    {
        public int version { get; set; }
        public int pid { get; set; }
        public bool running { get; set; }
        public string updated_at { get; set; }
        public string loaded_sha256 { get; set; }
        public string error { get; set; }
    }

    internal sealed class RulesStore
    {
        private const int Limit = 256 * 1024;
        internal readonly string FilePath;
        private readonly UTF8Encoding utf8 = new UTF8Encoding(false, true);
        internal RulesStore(string path) { FilePath = Path.GetFullPath(path); }

        private static string Hash(byte[] bytes)
        {
            using (var sha = SHA256.Create()) { return BitConverter.ToString(sha.ComputeHash(bytes)).Replace("-", "").ToLowerInvariant(); }
        }
        private static byte[] ReadBounded(string path, int limit)
        {
            using (var file = File.Open(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete))
            using (var result = new MemoryStream())
            {
                byte[] buffer = new byte[4096]; int count;
                while ((count = file.Read(buffer, 0, Math.Min(buffer.Length, limit + 1 - (int)result.Length))) > 0)
                {
                    result.Write(buffer, 0, count);
                    if (result.Length > limit) throw new InvalidOperationException("规则文件超过大小限制。");
                }
                return result.ToArray();
            }
        }
        private static string Domain(string input)
        {
            string value = (input ?? "").Trim().ToLowerInvariant();
            if (value.EndsWith(".", StringComparison.Ordinal)) value = value.Substring(0, value.Length - 1);
            System.Net.IPAddress ip;
            if (value.Length == 0 || value.Length > 253 || System.Net.IPAddress.TryParse(value, out ip))
                throw new InvalidOperationException("请输入有效域名，不能把 IP 放在域名类型中。");
            foreach (string label in value.Split('.'))
            {
                if (label.Length == 0 || label.Length > 63 || label[0] == '-' || label[label.Length - 1] == '-' ||
                    label.Any(c => !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-')))
                    throw new InvalidOperationException("域名需为 ASCII/Punycode，不带协议、路径、端口或 *。含子域名请选对应类型。");
            }
            return value;
        }
        private static string IPv4(string input)
        {
            string[] pieces = (input ?? "").Trim().Split('/');
            string[] parts = pieces[0].Split('.');
            int bits = 32;
            if (pieces.Length > 2 || parts.Length != 4 || (pieces.Length == 2 &&
                (!Int32.TryParse(pieces[1], NumberStyles.None, CultureInfo.InvariantCulture, out bits) || bits > 32)))
                throw new InvalidOperationException("请输入 IPv4 或 IPv4 网段，例如 10.20.26.253 或 10.20.0.0/16。");
            byte[] bytes = new byte[4];
            for (int i = 0; i < 4; i++)
            {
                if ((parts[i].Length > 1 && parts[i][0] == '0') || !Byte.TryParse(parts[i], NumberStyles.None, CultureInfo.InvariantCulture, out bytes[i]))
                    throw new InvalidOperationException("IPv4 格式无效；校园代理当前只支持 IPv4。");
                int keep = Math.Max(0, Math.Min(8, bits - i * 8));
                bytes[i] &= (byte)(255 << (8 - keep));
            }
            return String.Join(".", bytes.Select(b => b.ToString(CultureInfo.InvariantCulture))) + "/" + bits;
        }

        internal static RuleDocument Normalize(RuleDocument document)
        {
            if (document == null || (document.version != 1 && document.version != 2) || document.domains == null || document.domain_suffixes == null || document.ip_cidrs == null)
                throw new InvalidOperationException("规则文件需要 version=1 或 2 和三个完整数组。");
            if ((document.version == 1 && document.disabled != null) || (document.version == 2 && document.disabled == null))
                throw new InvalidOperationException("逐条启用开关需要 version=2 和完整的 disabled 对象。");
            var result = new RuleDocument
            {
                version = 2,
                domains = document.domains.Select(Domain).Distinct(StringComparer.Ordinal).ToArray(),
                domain_suffixes = document.domain_suffixes.Select(Domain).Distinct(StringComparer.Ordinal).ToArray(),
                ip_cidrs = document.ip_cidrs.Select(IPv4).Distinct(StringComparer.Ordinal).ToArray(),
                disabled = DisabledRuleDocument.Empty()
            };
            if (document.disabled != null)
            {
                var off = Normalize(new RuleDocument { version = 1, domains = document.disabled.domains, domain_suffixes = document.disabled.domain_suffixes, ip_cidrs = document.disabled.ip_cidrs });
                if (off.domains.Except(result.domains).Any() || off.domain_suffixes.Except(result.domain_suffixes).Any() || off.ip_cidrs.Except(result.ip_cidrs).Any())
                    throw new InvalidOperationException("未启用的条目必须存在于同类型的完整规则列表中。");
                result.disabled = new DisabledRuleDocument { domains = off.domains, domain_suffixes = off.domain_suffixes, ip_cidrs = off.ip_cidrs };
            }
            return result;
        }

        private static void ValidateArrays(Dictionary<string, object> root)
        {
            foreach (string key in new[] { "domains", "domain_suffixes", "ip_cidrs" })
            {
                object list;
                var values = root.TryGetValue(key, out list) ? list as object[] : null;
                if (values == null || values.Any(v => !(v is string))) throw new InvalidOperationException("规则列表必须是三个完整的字符串数组。");
            }
        }

        internal RuleSnapshot Load()
        {
            byte[] bytes = ReadBounded(FilePath, Limit);
            try
            {
                var json = new JavaScriptSerializer { MaxJsonLength = Limit };
                string text = utf8.GetString(bytes);
                var root = json.DeserializeObject(text) as Dictionary<string, object>;
                object version;
                if (root == null || !root.TryGetValue("version", out version) || !(version is int) || ((int)version != 1 && (int)version != 2) || root.Count != ((int)version == 1 ? 4 : 5))
                    throw new InvalidOperationException("规则 JSON 字段或版本不正确。");
                ValidateArrays(root);
                if ((int)version == 2)
                {
                    object off;
                    var disabled = root.TryGetValue("disabled", out off) ? off as Dictionary<string, object> : null;
                    if (disabled == null || disabled.Count != 3) throw new InvalidOperationException("未启用规则对象需要三个完整数组。");
                    ValidateArrays(disabled);
                }
                return new RuleSnapshot { Document = Normalize(json.ConvertToType<RuleDocument>(root)), Hash = Hash(bytes) };
            }
            catch (InvalidOperationException) { throw; }
            catch (Exception error) { throw new InvalidOperationException("规则 JSON 格式无效：" + error.Message, error); }
        }

        internal RuleSnapshot Save(RuleDocument document, string expectedHash)
        {
            RuleDocument normalized = Normalize(document);
            byte[] bytes = utf8.GetBytes(new JavaScriptSerializer { MaxJsonLength = Limit }.Serialize(normalized));
            if (bytes.Length > Limit) throw new InvalidOperationException("规则文件不能超过 256 KiB。");
            // Serialize the compare-and-replace operation across editor windows.
            string mutexName = @"Local\FduRulesSave-" + Hash(Encoding.UTF8.GetBytes(FilePath.ToUpperInvariant()));
            string temporary = Path.Combine(Path.GetDirectoryName(FilePath), ".fdu-rules-" + Guid.NewGuid().ToString("N") + ".tmp");
            try
            {
                // Finish potentially slow disk writes before checking the destination revision.
                using (var file = new FileStream(temporary, FileMode.CreateNew, FileAccess.Write, FileShare.None))
                {
                    file.Write(bytes, 0, bytes.Length);
                    file.Flush(true);
                }
                using (var mutex = new Mutex(false, mutexName))
                {
                    bool owned = false;
                    try
                    {
                        try { owned = mutex.WaitOne(2000); } catch (AbandonedMutexException) { owned = true; }
                        if (!owned) throw new InvalidOperationException("另一个窗口正在保存规则，请稍后重试。");
                        bool exists = File.Exists(FilePath);
                        string actualHash = exists ? Hash(ReadBounded(FilePath, Limit)) : null;
                        if (!String.Equals(expectedHash, actualHash, StringComparison.Ordinal))
                            throw new InvalidOperationException("规则文件已在其他位置修改或删除。请先重新读取，避免覆盖其他修改。");
                        if (exists) File.Replace(temporary, FilePath, null); else File.Move(temporary, FilePath);
                        return new RuleSnapshot { Document = normalized, Hash = Hash(bytes) };
                    }
                    finally { if (owned) mutex.ReleaseMutex(); }
                }
            }
            finally { if (File.Exists(temporary)) File.Delete(temporary); }
        }

        internal RulesApplyState GetApplyStatus(string expectedHash)
        {
            var waiting = new RulesApplyState { Message = "已保存，尚未收到运行中 FDU 的加载确认。首次需确认启用新版 FDU。" };
            if (expectedHash == null) return new RulesApplyState { Message = "规则尚未保存。" };
            try
            {
                var status = new JavaScriptSerializer().Deserialize<RuleStatus>(utf8.GetString(ReadBounded(FilePath + ".status.json", 16384)));
                DateTimeOffset timestamp;
                if (status == null || status.version != 1 || !status.running ||
                    !DateTimeOffset.TryParse(status.updated_at, CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind, out timestamp)) return waiting;
                double age = (DateTimeOffset.UtcNow - timestamp).TotalSeconds;
                if (age < -5 || age > 15) return waiting;
                using (var process = Process.GetProcessById(status.pid))
                {
                    if (process.HasExited || process.StartTime.ToUniversalTime() > timestamp.UtcDateTime) return waiting;
                }
                if (!String.IsNullOrEmpty(status.error)) return new RulesApplyState { Message = "FDU 未接受新规则，仍保留上一份有效规则。\r\n" + status.error };
                if (!String.Equals(status.loaded_sha256, expectedHash, StringComparison.OrdinalIgnoreCase)) return new RulesApplyState { Message = "已保存，等待 FDU 加载新规则…" };
                return new RulesApplyState { Applied = true, Message = "FDU 已加载当前规则，新连接按此分流。已有连接保留原路径。" };
            }
            catch (Exception) { return waiting; }
        }
    }
}
