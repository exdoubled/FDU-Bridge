using System;
using System.Collections.Generic;
using System.IO;
using System.IO.Pipes;
using System.Linq;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;
using System.Windows.Forms;

namespace FduNodePicker
{
    // Real client against isolated local pipes. These tests never discover the live controller.
    internal sealed class FakeController : IDisposable
    {
        internal const string Japan = "🇯🇵 日本 / \"测试\"";
        internal const string HongKong = "🇭🇰 香港";
        internal readonly string PipeName = "fdu-picker-test-" + Guid.NewGuid().ToString("N");
        internal readonly List<string> Requests = new List<string>();
        internal string Mode = "global", Global = "FDU-ROUTER", Current = Japan;
        internal string[] Members = { Japan, HongKong };
        internal bool UnsafeGroup, Chunked, IgnoreSelection;
        internal string WrittenName;
        private volatile bool stopped;
        private NamedPipeServerStream server;
        private readonly Thread thread;
        private Exception failure;
        private readonly JavaScriptSerializer json = new JavaScriptSerializer();

        internal FakeController()
        {
            thread = new Thread(Serve);
            thread.IsBackground = true;
            thread.Start();
        }

        internal PickerClient Client(int timeout)
        {
            return new PickerClient(new PipeApi(PipeName, timeout));
        }

        private void Serve()
        {
            try
            {
                while (!stopped)
                {
                    using (var pipe = new NamedPipeServerStream(PipeName, PipeDirection.InOut, 1,
                        PipeTransmissionMode.Byte, PipeOptions.Asynchronous))
                    {
                        server = pipe;
                        if (stopped) return;
                        pipe.WaitForConnection();
                        var header = new List<byte>();
                        while (header.Count < 65536)
                        {
                            int b = pipe.ReadByte();
                            if (b < 0) throw new IOException("Request ended in headers");
                            header.Add((byte)b);
                            int n = header.Count;
                            if (n >= 4 && header[n - 4] == 13 && header[n - 3] == 10 && header[n - 2] == 13 && header[n - 1] == 10) break;
                        }
                        string[] lines = Encoding.ASCII.GetString(header.ToArray()).Split(new[] { "\r\n" }, StringSplitOptions.None);
                        string request = lines[0];
                        int size = Int32.Parse(lines.Single(l => l.StartsWith("Content-Length:")).Substring(15).Trim());
                        byte[] body = new byte[size];
                        for (int i = 0; i < size; )
                        {
                            int n = pipe.Read(body, i, size - i);
                            if (n == 0) throw new IOException("Request ended in body");
                            i += n;
                        }
                        Requests.Add(request);
                        int status = 200;
                        object response = null;
                        if (request == "GET /configs HTTP/1.1") response = new { mode = Mode };
                        else if (request == "GET /proxies HTTP/1.1")
                        {
                            var proxies = new Dictionary<string, object>();
                            proxies["GLOBAL"] = new { type = "Selector", now = Global, all = new[] { "FDU-ROUTER", Japan } };
                            proxies["INTERNET"] = new { type = "Selector", now = Current, all = Members };
                            proxies[Japan] = new { type = "Shadowsocks" };
                            proxies[HongKong] = new { type = "Shadowsocks" };
                            proxies["FDU-ROUTER"] = new { type = "Socks5" };
                            if (UnsafeGroup) proxies["循环组"] = new { type = "Selector", now = Japan, all = new[] { Japan, "FDU-ROUTER" } };
                            response = new { proxies = proxies };
                        }
                        else if (request == "PUT /proxies/INTERNET HTTP/1.1")
                        {
                            var data = json.Deserialize<Dictionary<string, string>>(Encoding.UTF8.GetString(body));
                            if (data.Count != 1 || !data.ContainsKey("name")) throw new Exception("Unexpected write payload");
                            WrittenName = data["name"];
                            if (!IgnoreSelection) Current = WrittenName;
                            status = 204;
                        }
                        else throw new Exception("Unexpected request: " + request);
                        byte[] payload = response == null ? new byte[0] : Encoding.UTF8.GetBytes(json.Serialize(response));
                        string framing = Chunked && status == 200 ? "Transfer-Encoding: chunked\r\n" : "Content-Length: " + payload.Length + "\r\n";
                        byte[] start = Encoding.ASCII.GetBytes("HTTP/1.1 " + status + " Test\r\n" + framing + "Connection: close\r\n\r\n");
                        pipe.Write(start, 0, start.Length);
                        if (Chunked && status == 200)
                        {
                            for (int i = 0; i < payload.Length; i += 17)
                            {
                                int count = Math.Min(17, payload.Length - i);
                                byte[] chunkHeader = Encoding.ASCII.GetBytes(count.ToString("x") + ";test=yes\r\n");
                                pipe.Write(chunkHeader, 0, chunkHeader.Length);
                                pipe.Write(payload, i, count);
                                pipe.Write(new byte[] { 13, 10 }, 0, 2);
                            }
                            byte[] end = Encoding.ASCII.GetBytes("0\r\n\r\n");
                            pipe.Write(end, 0, end.Length);
                        }
                        else pipe.Write(payload, 0, payload.Length);
                    }
                }
            }
            catch (Exception error) { if (!stopped) failure = error; }
        }

        public void Dispose()
        {
            stopped = true;
            if (server != null) server.Dispose();
            thread.Join(1500);
            if (failure != null) throw new Exception("Fixture failed", failure);
        }
    }

    internal static class PickerTests
    {
        private static int failures, count;
        private static void Check(bool condition, string message)
        {
            if (!condition) throw new Exception(message);
        }
        private static void Reject(Action action, string message)
        {
            try { action(); }
            catch (InvalidOperationException) { return; }
            throw new Exception(message);
        }
        private static void Run(string name, Action action)
        {
            try { count++; action(); Console.WriteLine("PASS " + name); }
            catch (Exception e) { failures++; Console.WriteLine("FAIL " + name + ": " + e); }
        }
        [STAThread]
        internal static int Main()
        {
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Run("Opening and refreshing only read", delegate
            {
                using (var f = new FakeController())
                {
                    var client = f.Client(3000);
                    var state = client.Load();
                    client.Load();
                    Check(state.Problem == null && state.Current == FakeController.Japan && state.Choices.Contains(FakeController.HongKong), "Incorrect state");
                    Check(f.Requests.Count == 4 && f.Requests.All(r => r.StartsWith("GET ")), "Load wrote state");
                }
            });
            Run("Unicode selection writes only INTERNET and preserves mode/GLOBAL", delegate
            {
                using (var f = new FakeController())
                {
                    f.Current = FakeController.HongKong;
                    var state = f.Client(3000).Select(FakeController.Japan);
                    Check(state.Current == FakeController.Japan && f.WrittenName == FakeController.Japan, "Unicode body corrupted");
                    Check(f.Mode == "global" && f.Global == "FDU-ROUTER", "Route changed");
                    Check(f.Requests.Count(r => !r.StartsWith("GET ")) == 1 && f.Requests.Contains("PUT /proxies/INTERNET HTTP/1.1"), "Unexpected write");
                }
            });
            Run("Mode changed after list load blocks selection", delegate
            {
                using (var f = new FakeController())
                {
                    var client = f.Client(3000); client.Load(); f.Mode = "rule";
                    Reject(delegate { client.Select(FakeController.HongKong); }, "Accepted changed mode");
                    Check(f.WrittenName == null, "Wrote despite changed mode");
                }
            });
            Run("GLOBAL changed after list load blocks selection", delegate
            {
                using (var f = new FakeController())
                {
                    var client = f.Client(3000); client.Load(); f.Global = "DIRECT";
                    Reject(delegate { client.Select(FakeController.HongKong); }, "Accepted changed GLOBAL");
                    Check(f.WrittenName == null, "Repaired GLOBAL or wrote node");
                }
            });
            Run("Nested group reaching FDU is rejected even when its current node is ordinary", delegate
            {
                using (var f = new FakeController())
                {
                    f.UnsafeGroup = true; f.Members = new[] { FakeController.Japan, "循环组", "FDU-ROUTER", "INTERNET" };
                    var client = f.Client(3000);
                    Check(!client.Load().Choices.Contains("循环组"), "Unsafe group visible as selectable");
                    Reject(delegate { client.Select("循环组"); }, "Accepted unsafe group");
                    Reject(delegate { client.Select("FDU-ROUTER"); }, "Accepted FDU");
                    Reject(delegate { client.Select("INTERNET"); }, "Accepted self-loop");
                    Check(f.WrittenName == null, "Wrote unsafe target");
                }
            });
            Run("Chunked responses preserve UTF-8 across chunk boundaries", delegate
            {
                using (var f = new FakeController())
                {
                    f.Chunked = true;
                    Check(f.Client(3000).Load().Current == FakeController.Japan, "Chunk framing corrupted Unicode");
                }
            });
            Run("Unconfirmed write is not retried or silently declared successful", delegate
            {
                using (var f = new FakeController())
                {
                    f.IgnoreSelection = true;
                    Reject(delegate { f.Client(3000).Select(FakeController.HongKong); }, "Declared unconfirmed selection successful");
                    Check(f.Requests.Count(r => r.StartsWith("PUT ")) == 1, "Retried write");
                }
            });
            failures += RulesTests.RunAll();
            Console.WriteLine(failures == 0 ? "All " + (count + RulesTests.Count) + " tests passed." : failures + " tests failed.");
            return failures == 0 ? 0 : 1;
        }
    }
}
