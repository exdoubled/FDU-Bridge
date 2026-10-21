using System;
using System.Drawing;
using System.IO;
using System.Linq;
using System.Threading.Tasks;
using System.Windows.Forms;

namespace FduNodePicker
{
    internal sealed class PickerWindow : Form
    {
        private readonly PickerClient client;
        private readonly Label route = new Label(), current = new Label(), status = new Label(), count = new Label();
        private readonly TextBox filter = new TextBox();
        private readonly ListBox nodes = new ListBox();
        private readonly Button refresh = new Button(), select = new Button();
        private PickerState state;
        private bool busy;

        internal PickerWindow(PickerClient client) : this(client, new RulesStore(Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "routing-rules.json"))) { }

        internal PickerWindow(PickerClient client, RulesStore rulesStore)
        {
            this.client = client;
            Text = "FDU 节点与规则";
            Font = new Font("Microsoft YaHei UI", 9F);
            AutoScaleMode = AutoScaleMode.Font;
            StartPosition = FormStartPosition.CenterScreen;
            ClientSize = new Size(720, 560);
            MinimumSize = new Size(680, 540);
            Icon = SystemIcons.Application;
            var tabs = new TabControl { Name = "MainTabs", Dock = DockStyle.Fill };
            var nodePage = new TabPage("外网节点");
            var rulesPage = new TabPage("FDU 规则");
            var rulesPanel = new RulesPanel(rulesStore) { Name = "RulesPanel", Dock = DockStyle.Fill };
            rulesPage.Controls.Add(rulesPanel);
            tabs.TabPages.Add(nodePage);
            tabs.TabPages.Add(rulesPage);
            Controls.Add(tabs);
            var layout = new TableLayoutPanel { Dock = DockStyle.Fill, Padding = new Padding(18), ColumnCount = 1, RowCount = 8 };
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 35));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 30));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 35));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 38));
            layout.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 30));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 44));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 66));
            nodePage.Controls.Add(layout);
            layout.Controls.Add(new Label { Text = "选择外网节点", Font = new Font(Font.FontFamily, 14F, FontStyle.Bold), Dock = DockStyle.Fill }, 0, 0);
            route.Text = "正在读取 Clash 状态…";
            route.Dock = DockStyle.Fill;
            route.AutoEllipsis = true;
            layout.Controls.Add(route, 0, 1);
            current.Name = "CurrentNode";
            current.Text = "当前外网：读取中";
            current.Font = new Font(Font, FontStyle.Bold);
            current.Dock = DockStyle.Fill;
            current.AutoEllipsis = true;
            layout.Controls.Add(current, 0, 2);
            var search = new TableLayoutPanel { Dock = DockStyle.Fill, ColumnCount = 2, RowCount = 1, Margin = Padding.Empty };
            search.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 50));
            search.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
            search.Controls.Add(new Label { Text = "搜索", Dock = DockStyle.Fill, TextAlign = ContentAlignment.MiddleLeft }, 0, 0);
            filter.Name = "Filter";
            filter.Dock = DockStyle.Fill;
            filter.TabIndex = 0;
            filter.TextChanged += delegate { FilterNodes(); };
            search.Controls.Add(filter, 1, 0);
            layout.Controls.Add(search, 0, 3);
            nodes.Name = "NodeList";
            nodes.Dock = DockStyle.Fill;
            nodes.IntegralHeight = false;
            nodes.HorizontalScrollbar = true;
            nodes.TabIndex = 1;
            nodes.SelectedIndexChanged += delegate { UpdateButtons(); };
            nodes.DoubleClick += async delegate { await SwitchSelectedAsync(); };
            layout.Controls.Add(nodes, 0, 4);
            count.Dock = DockStyle.Fill;
            count.ForeColor = SystemColors.GrayText;
            count.TextAlign = ContentAlignment.MiddleLeft;
            layout.Controls.Add(count, 0, 5);
            var buttons = new FlowLayoutPanel { Dock = DockStyle.Fill, FlowDirection = FlowDirection.RightToLeft, WrapContents = false, Margin = Padding.Empty };
            select.Name = "SwitchButton";
            select.Text = "切换到所选节点";
            select.Size = new Size(160, 34);
            select.BackColor = Color.FromArgb(35, 100, 205);
            select.ForeColor = Color.White;
            select.FlatStyle = FlatStyle.Flat;
            select.TabIndex = 2;
            select.Click += async delegate { await SwitchSelectedAsync(); };
            refresh.Text = "刷新";
            refresh.Size = new Size(85, 34);
            refresh.TabIndex = 3;
            refresh.Click += async delegate { await RefreshNodesAsync(); };
            buttons.Controls.Add(select);
            buttons.Controls.Add(refresh);
            layout.Controls.Add(buttons, 0, 6);
            status.Name = "StatusText";
            status.Text = "打开和刷新只读取状态。选好节点后，点击切换按钮。";
            status.Dock = DockStyle.Fill;
            status.Padding = new Padding(0, 5, 0, 0);
            layout.Controls.Add(status, 0, 7);
            Shown += async delegate { await RefreshNodesAsync(); };
            FormClosing += delegate(object sender, FormClosingEventArgs args)
            {
                if (rulesPanel.Dirty && MessageBox.Show(this, "FDU 规则有未保存的修改，确定关闭并丢弃草稿吗？", "未保存的规则", MessageBoxButtons.YesNo, MessageBoxIcon.Question) != DialogResult.Yes)
                    args.Cancel = true;
            };
            UpdateButtons();
        }

        private void UpdateButtons()
        {
            refresh.Enabled = !busy;
            filter.Enabled = !busy;
            nodes.Enabled = !busy;
            select.Enabled = !busy && state != null && state.Problem == null && nodes.SelectedItem != null;
        }

        private void FilterNodes()
        {
            string previous = nodes.SelectedItem as string;
            nodes.BeginUpdate();
            try
            {
                nodes.Items.Clear();
                if (state != null)
                {
                    nodes.Items.AddRange(state.Choices.Where(n => n.IndexOf(filter.Text.Trim(), StringComparison.OrdinalIgnoreCase) >= 0).ToArray());
                    if (previous != null && nodes.Items.Contains(previous)) nodes.SelectedItem = previous;
                    else if (nodes.Items.Contains(state.Current)) nodes.SelectedItem = state.Current;
                    else if (nodes.Items.Count > 0) nodes.SelectedIndex = 0;
                    count.Text = "显示 " + nodes.Items.Count + " / " + state.Choices.Length + " 个选项 · 双击节点也可切换";
                }
                else count.Text = "请点击刷新重新读取节点";
            }
            finally { nodes.EndUpdate(); }
            UpdateButtons();
        }

        private void ShowState(PickerState loaded)
        {
            state = loaded;
            string mode = state.Mode == "global" ? "全局" : state.Mode == "rule" ? "规则" : state.Mode == "direct" ? "直连" : state.Mode;
            route.Text = "模式：" + mode + "    |    GLOBAL：" + state.Global;
            current.Text = "当前外网：" + state.Current;
            FilterNodes();
        }

        private void ShowStatus(string text, bool error)
        {
            status.Text = text;
            status.ForeColor = error ? Color.Firebrick : SystemColors.ControlText;
        }

        internal async Task RefreshNodesAsync()
        {
            if (busy || IsDisposed) return;
            busy = true;
            UpdateButtons();
            ShowStatus("正在读取节点…", false);
            try
            {
                var loaded = await Task.Run(() => client.Load());
                if (IsDisposed) return;
                ShowState(loaded);
                ShowStatus(loaded.Problem ?? "选好后点击切换。只切换外网节点；已有连接可能继续使用原节点。", loaded.Problem != null);
            }
            catch (Exception error)
            {
                if (IsDisposed) return;
                state = null;
                route.Text = "未能读取 Clash 状态";
                current.Text = "当前外网：未知";
                FilterNodes();
                ShowStatus(error.Message, true);
            }
            finally { busy = false; if (!IsDisposed) UpdateButtons(); }
        }

        internal async Task SwitchSelectedAsync()
        {
            if (busy || IsDisposed || state == null || state.Problem != null || nodes.SelectedItem == null) return;
            string name = (string)nodes.SelectedItem;
            busy = true;
            UpdateButtons();
            ShowStatus("正在切换并确认结果…", false);
            try
            {
                var loaded = await Task.Run(() => client.Select(name));
                if (IsDisposed) return;
                ShowState(loaded);
                ShowStatus("已切换到：" + loaded.Current + "\r\n新连接使用该节点；已有连接可能继续使用原节点。", false);
            }
            catch (Exception error)
            {
                if (IsDisposed) return;
                state = null;
                current.Text = "当前外网：请刷新确认";
                ShowStatus(error.Message, true);
            }
            finally { busy = false; if (!IsDisposed) UpdateButtons(); }
        }
    }

    internal static class Program
    {
        [STAThread]
        private static void Main()
        {
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);
            Application.Run(new PickerWindow(new PickerClient()));
        }
    }
}
