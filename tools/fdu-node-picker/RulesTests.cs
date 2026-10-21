using System;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Text;
using System.Web.Script.Serialization;
using System.Windows.Forms;

namespace FduNodePicker
{
    internal static class RulesTests
    {
        internal static int Count;
        private static int failures;
        private const string Seed = "{\"version\":1,\"domains\":[],\"domain_suffixes\":[],\"ip_cidrs\":[\"10.20.26.253/32\"]}";
        private static void Check(bool value, string message) { if (!value) throw new Exception(message); }
        private static void Reject(Action action)
        {
            try { action(); } catch (InvalidOperationException) { return; }
            throw new Exception("Invalid input/action was accepted");
        }
        private static void Run(string name, Action<RulesStore, string> action)
        {
            Count++;
            string directory = System.IO.Path.Combine(System.IO.Path.GetTempPath(), "fdu-rules-test-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(directory);
            try
            {
                string path = System.IO.Path.Combine(directory, "rules.json");
                File.WriteAllText(path, Seed, new UTF8Encoding(false));
                action(new RulesStore(path), path);
                Console.WriteLine("PASS " + name);
            }
            catch (Exception error) { failures++; Console.WriteLine("FAIL " + name + ": " + error); }
            finally { Directory.Delete(directory, true); }
        }
        private static void Status(string path, string hash, DateTime updated, bool running, string error, int pid)
        {
            File.WriteAllText(path + ".status.json", new JavaScriptSerializer().Serialize(new {
                version = 1, pid = pid, running = running, updated_at = updated.ToString("o"), loaded_sha256 = hash, error = error
            }), new UTF8Encoding(false));
        }
        internal static int RunAll()
        {
            Run("Rule normalization preserves domain boundaries and masks IPv4", delegate(RulesStore store, string path)
            {
                var document = new RuleDocument { version = 1, domains = new[] { " Exact.Fudan.edu.cn. ", "exact.fudan.edu.cn" }, domain_suffixes = new[] { "Lab.Fudan.edu.cn" }, ip_cidrs = new[] { "10.20.26.253/24", "10.20.26.0/24", "10.2.3.4" } };
                var value = RulesStore.Normalize(document);
                Check(value.domains.SequenceEqual(new[] { "exact.fudan.edu.cn" }) && value.domain_suffixes[0] == "lab.fudan.edu.cn", "Domain normalization changed meaning");
                Check(value.ip_cidrs.SequenceEqual(new[] { "10.20.26.0/24", "10.2.3.4/32" }), "IP mask/duplicates wrong");
            });
            Run("URLs, wildcard domains, non-IPv4 and invalid masks are rejected", delegate(RulesStore store, string path)
            {
                foreach (string value in new[] { "https://fudan.edu.cn/a", "*.fudan.edu.cn", "a..fudan.edu.cn", "中文.edu.cn", "10.1.2.3" })
                {
                    var d = RuleDocument.Empty(); d.domains = new[] { value }; Reject(delegate { RulesStore.Normalize(d); });
                }
                foreach (string value in new[] { "::1", "10.1.2.3/33", "10.1.2.256", "127.1", "10.1.2.3/-1" })
                {
                    var d = RuleDocument.Empty(); d.ip_cidrs = new[] { value }; Reject(delegate { RulesStore.Normalize(d); });
                }
            });
            Run("Missing arrays, wrong version, unknown fields and oversized JSON are rejected", delegate(RulesStore store, string path)
            {
                foreach (string text in new[] { "{}", "{\"version\":1}", "{\"version\":1,\"domains\":null,\"domain_suffixes\":[],\"ip_cidrs\":[]}", Seed.Replace("\"version\":1", "\"version\":2"), Seed.TrimEnd('}') + ",\"x\":true}", Seed + "{}",
                    Seed.Replace("\"domains\":[]", "\"domains\":[123]"),
                    Seed.Replace("\"domains\":[]", "\"domains\":[null]"),
                    Seed.Replace("\"domain_suffixes\":[]", "\"domain_suffixes\":\"lab.fudan.edu.cn\""),
                    Seed.Replace("\"10.20.26.253/32\"", "{}") })
                {
                    File.WriteAllText(path, text, new UTF8Encoding(false)); Reject(delegate { store.Load(); });
                }
                File.WriteAllBytes(path, new byte[256 * 1024 + 1]); Reject(delegate { store.Load(); });
            });
            Run("Changed or deleted files are not silently overwritten", delegate(RulesStore store, string path)
            {
                var before = store.Load();
                File.WriteAllText(path, Seed + " ", new UTF8Encoding(false));
                Reject(delegate { store.Save(before.Document, before.Hash); });
                Check(File.ReadAllText(path).EndsWith(" "), "External change lost");
                File.Delete(path);
                Reject(delegate { store.Save(before.Document, before.Hash); });
                Check(!File.Exists(path), "Deleted file resurrected");
            });
            Run("Stale, stopped, rejected or mismatched acknowledgements do not claim applied", delegate(RulesStore store, string path)
            {
                string hash = store.Load().Hash; int pid = Process.GetCurrentProcess().Id;
                Check(!store.GetApplyStatus(hash).Applied, "Offline state claims applied");
                Status(path, hash, DateTime.UtcNow, true, "", pid);
                Check(store.GetApplyStatus(hash).Applied, "Fresh acknowledgement ignored");
                Status(path, hash, DateTime.UtcNow.AddMinutes(-1), true, "", pid); Check(!store.GetApplyStatus(hash).Applied, "Stale ack accepted");
                Status(path, hash, DateTime.UtcNow, false, "", pid); Check(!store.GetApplyStatus(hash).Applied, "Stopped ack accepted");
                Status(path, hash, DateTime.UtcNow, true, "invalid JSON", pid); Check(!store.GetApplyStatus(hash).Applied, "Rejected ack accepted");
                Status(path, "other", DateTime.UtcNow, true, "", pid); Check(!store.GetApplyStatus(hash).Applied, "Different revision accepted");
                Status(path, hash, DateTime.UtcNow, true, "", Int32.MaxValue); Check(!store.GetApplyStatus(hash).Applied, "Exited PID accepted");
                Status(path, hash, DateTime.UtcNow.AddHours(1), true, "", pid); Check(!store.GetApplyStatus(hash).Applied, "Future ack accepted");
            });
            Run("Disabled rules roundtrip without deleting data or crossing rule types", delegate(RulesStore store, string path)
            {
                var before = store.Load(); var d = RuleDocument.Empty();
                d.domains = new[] { "lab.fudan.edu.cn" }; d.domain_suffixes = new[] { "lab.fudan.edu.cn" };
                d.ip_cidrs = new[] { "10.0.0.0/8", "10.20.26.253" };
                d.disabled.domains = new[] { "LAB.FUDAN.EDU.CN" }; d.disabled.ip_cidrs = new[] { "10.0.0.0/8" };
                store.Save(d, before.Hash);
                var saved = store.Load().Document;
                Check(saved.version == 2 && saved.domains.Length == 1 && saved.ip_cidrs.Length == 2, "Disabled content lost");
                Check(saved.disabled.domains[0] == "lab.fudan.edu.cn" && saved.disabled.domain_suffixes.Length == 0 && saved.disabled.ip_cidrs[0] == "10.0.0.0/8", "Typed switches changed meaning");
            });
            Run("Checkbox edits only a draft; save and reload retain disabled contents", delegate(RulesStore store, string path)
            {
                byte[] original = File.ReadAllBytes(path);
                using (var panel = new RulesPanel(store))
                {
                    var grid = (DataGridView)panel.Controls.Find("RulesGrid", true).Single();
                    Check((bool)grid.Rows[0].Cells["Enabled"].Value, "Initial checkbox not enabled");
                    grid.Rows[0].Cells["Enabled"].Value = false;
                    Check(panel.Dirty && File.ReadAllBytes(path).SequenceEqual(original), "Checkbox wrote before Save");
                    panel.SaveRules();
                    Check(store.Load().Document.ip_cidrs[0] == "10.20.26.253/32" && store.Load().Document.disabled.ip_cidrs[0] == "10.20.26.253/32", "Unchecked rule lost/not disabled");
                    panel.ReloadRules();
                    Check(!(bool)grid.Rows[0].Cells["Enabled"].Value && !panel.Dirty, "Saved checkbox not restored");
                    grid.Rows[0].Cells["Enabled"].Value = true; panel.SaveRules();
                    Check(store.Load().Document.disabled.ip_cidrs.Length == 0, "Re-enable did not clear disabled flag");
                }
            });
            return failures;
        }
    }
}
