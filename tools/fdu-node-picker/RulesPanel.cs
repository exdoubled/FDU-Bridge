using System;
using System.Collections.Generic;
using System.Drawing;
using System.IO;
using System.Linq;
using System.Windows.Forms;

namespace FduNodePicker
{
    internal sealed class RulesPanel : UserControl
    {
        private readonly RulesStore store;
        private readonly DataGridView grid = new DataGridView();
        private readonly ComboBox kind = new ComboBox();
        private readonly TextBox value = new TextBox();
        private readonly Label status = new Label();
        private readonly Button add = new Button(), remove = new Button(), save = new Button();
        private readonly Timer timer = new Timer { Interval = 2000 };
        private readonly string[] kinds = { "精确域名", "域名及子域名", "IPv4 / 网段" };
        private RuleDocument draft = RuleDocument.Empty();
        private string savedHash;
        private bool readable;
        private bool rendering;
        internal bool Dirty { get; private set; }

        internal RulesPanel(RulesStore store)
        {
            this.store = store;
            var layout = new TableLayoutPanel { Dock = DockStyle.Fill, Padding = new Padding(18), ColumnCount = 1, RowCount = 6 };
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 42));
            layout.RowStyles.Add(new RowStyle(SizeType.Percent, 100));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 42));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 44));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 44));
            layout.RowStyles.Add(new RowStyle(SizeType.Absolute, 100));
            Controls.Add(layout);
            layout.Controls.Add(new Label { Text = "只有匹配已勾选规则的目标走 FDU 校园代理，其他流量使用外网节点。", Dock = DockStyle.Fill }, 0, 0);
            grid.Name = "RulesGrid";
            grid.Dock = DockStyle.Fill;
            grid.AllowUserToAddRows = false;
            grid.AllowUserToDeleteRows = false;
            grid.ReadOnly = false;
            grid.MultiSelect = true;
            grid.SelectionMode = DataGridViewSelectionMode.FullRowSelect;
            grid.RowHeadersVisible = false;
            grid.AutoSizeColumnsMode = DataGridViewAutoSizeColumnsMode.Fill;
            grid.BackgroundColor = SystemColors.Window;
            grid.Columns.Add(new DataGridViewCheckBoxColumn { Name = "Enabled", HeaderText = "启用", Width = 55, AutoSizeMode = DataGridViewAutoSizeColumnMode.None, ThreeState = false });
            grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "Type", HeaderText = "类型", ReadOnly = true, FillWeight = 30, SortMode = DataGridViewColumnSortMode.NotSortable });
            grid.Columns.Add(new DataGridViewTextBoxColumn { Name = "Value", HeaderText = "域名 / IP / 网段", ReadOnly = true, FillWeight = 70, SortMode = DataGridViewColumnSortMode.NotSortable });
            grid.CurrentCellDirtyStateChanged += delegate
            {
                if (grid.IsCurrentCellDirty && grid.CurrentCell != null && grid.CurrentCell.ColumnIndex == 0)
                    grid.CommitEdit(DataGridViewDataErrorContexts.Commit);
            };
            grid.CellValueChanged += delegate(object sender, DataGridViewCellEventArgs args)
            {
                if (rendering || !readable || args.RowIndex < 0 || args.ColumnIndex != 0) return;
                draft = ReadDraft(grid.Rows.Cast<DataGridViewRow>());
                Dirty = true; UpdateButtons(); RefreshApplyStatus();
            };
            grid.SelectionChanged += delegate { UpdateButtons(); };
            layout.Controls.Add(grid, 0, 1);
            var input = new TableLayoutPanel { Dock = DockStyle.Fill, ColumnCount = 3, Margin = new Padding(0, 7, 0, 0) };
            input.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 155));
            input.ColumnStyles.Add(new ColumnStyle(SizeType.Percent, 100));
            input.ColumnStyles.Add(new ColumnStyle(SizeType.Absolute, 75));
            kind.DropDownStyle = ComboBoxStyle.DropDownList;
            kind.Items.AddRange(kinds);
            kind.SelectedIndex = 0;
            kind.Dock = DockStyle.Fill;
            value.Dock = DockStyle.Fill;
            value.Name = "RuleValue";
            add.Text = "添加";
            add.Dock = DockStyle.Fill;
            add.Click += delegate { TryAction(delegate { AddRule(kind.SelectedIndex, value.Text); value.Clear(); value.Focus(); }); };
            input.Controls.Add(kind, 0, 0); input.Controls.Add(value, 1, 0); input.Controls.Add(add, 2, 0);
            layout.Controls.Add(input, 0, 2);
            layout.Controls.Add(new Label { Text = "取消勾选会保留规则；若仍匹配其他已启用项，仍走 FDU。\r\n勾选和增删均需点击“保存规则”提交。", Dock = DockStyle.Fill, ForeColor = SystemColors.GrayText }, 0, 3);
            var buttons = new FlowLayoutPanel { Dock = DockStyle.Fill, FlowDirection = FlowDirection.RightToLeft, WrapContents = false, Margin = Padding.Empty };
            save.Text = "保存规则"; save.Name = "SaveRules";
            save.Size = new Size(110, 34);
            save.BackColor = Color.FromArgb(35, 100, 205); save.ForeColor = Color.White; save.FlatStyle = FlatStyle.Flat;
            save.Click += delegate { TryAction(SaveRules); };
            remove.Text = "删除选中"; remove.Size = new Size(100, 34);
            remove.Click += delegate { TryAction(RemoveSelectedRules); };
            var refresh = new Button { Text = "重新读取", Size = new Size(100, 34) };
            refresh.Click += delegate
            {
                if (!Dirty || MessageBox.Show(this, "重新读取将丢弃未保存的草稿，是否继续？", "未保存的规则", MessageBoxButtons.YesNo, MessageBoxIcon.Question) == DialogResult.Yes)
                    ReloadRules();
            };
            buttons.Controls.Add(save); buttons.Controls.Add(remove); buttons.Controls.Add(refresh);
            layout.Controls.Add(buttons, 0, 4);
            status.Name = "RulesStatus"; status.Dock = DockStyle.Fill; status.Padding = new Padding(0, 6, 0, 0);
            layout.Controls.Add(status, 0, 5);
            timer.Tick += delegate { RefreshApplyStatus(); };
            Load += delegate { timer.Start(); };
            ReloadRules();
        }

        protected override void Dispose(bool disposing)
        {
            if (disposing) timer.Dispose();
            base.Dispose(disposing);
        }

        private void TryAction(Action action)
        {
            try { action(); }
            catch (Exception error) { status.Text = error.Message; status.ForeColor = Color.Firebrick; }
        }

        private void UpdateButtons()
        {
            add.Enabled = readable;
            value.Enabled = readable;
            kind.Enabled = readable;
            grid.Enabled = readable;
            save.Enabled = readable && Dirty;
            remove.Enabled = readable && grid.SelectedRows.Count > 0;
        }

        private void RenderDraft()
        {
            rendering = true;
            try
            {
                grid.Rows.Clear();
                foreach (string item in draft.domains) grid.Rows.Add(!draft.disabled.domains.Contains(item), kinds[0], item);
                foreach (string item in draft.domain_suffixes) grid.Rows.Add(!draft.disabled.domain_suffixes.Contains(item), kinds[1], item);
                foreach (string item in draft.ip_cidrs) grid.Rows.Add(!draft.disabled.ip_cidrs.Contains(item), kinds[2], item);
            }
            finally { rendering = false; }
            UpdateButtons();
            RefreshApplyStatus();
        }

        internal void ReloadRules()
        {
            try
            {
                var loaded = store.Load();
                draft = loaded.Document; savedHash = loaded.Hash; readable = true; Dirty = false;
                RenderDraft();
            }
            catch (FileNotFoundException)
            {
                draft = RuleDocument.Empty(); savedHash = null; readable = true; Dirty = false;
                RenderDraft();
                status.Text = "尚无规则文件。可添加后保存；首次启用新版 FDU 前不会影响当前网络。";
            }
            catch (Exception error)
            {
                readable = false; Dirty = false;
                UpdateButtons(); status.Text = "无法读取规则：" + error.Message; status.ForeColor = Color.Firebrick;
            }
        }

        internal void AddRule(int type, string input)
        {
            if (!readable) throw new InvalidOperationException("请先读取有效规则文件。");
            grid.EndEdit();
            var candidate = new RuleDocument { version = 2, domains = draft.domains, domain_suffixes = draft.domain_suffixes, ip_cidrs = draft.ip_cidrs, disabled = draft.disabled };
            if (type == 0) candidate.domains = candidate.domains.Concat(new[] { input }).ToArray();
            else if (type == 1) candidate.domain_suffixes = candidate.domain_suffixes.Concat(new[] { input }).ToArray();
            else candidate.ip_cidrs = candidate.ip_cidrs.Concat(new[] { input }).ToArray();
            candidate = RulesStore.Normalize(candidate);
            if (candidate.domains.Length == draft.domains.Length && candidate.domain_suffixes.Length == draft.domain_suffixes.Length && candidate.ip_cidrs.Length == draft.ip_cidrs.Length)
            {
                status.Text = "该规则已存在。"; return;
            }
            draft = candidate; Dirty = true; RenderDraft();
        }

        internal void RemoveSelectedRules()
        {
            grid.EndEdit();
            var selected = grid.SelectedRows.Cast<DataGridViewRow>().ToArray();
            if (selected.Length == 0) return;
            var remaining = grid.Rows.Cast<DataGridViewRow>().Except(selected).ToArray();
            draft = ReadDraft(remaining);
            Dirty = true; RenderDraft();
        }

        private RuleDocument ReadDraft(IEnumerable<DataGridViewRow> input)
        {
            var rows = input.ToArray();
            var disabled = rows.Where(row => !Convert.ToBoolean(row.Cells["Enabled"].Value)).ToArray();
            Func<DataGridViewRow[], int, string[]> values = (list, type) => list.Where(row => (string)row.Cells["Type"].Value == kinds[type]).Select(row => (string)row.Cells["Value"].Value).ToArray();
            return new RuleDocument { version = 2,
                domains = values(rows, 0), domain_suffixes = values(rows, 1), ip_cidrs = values(rows, 2),
                disabled = new DisabledRuleDocument { domains = values(disabled, 0), domain_suffixes = values(disabled, 1), ip_cidrs = values(disabled, 2) } };
        }

        internal void SaveRules()
        {
            grid.EndEdit();
            if (!Dirty) return;
            var saved = store.Save(draft, savedHash);
            draft = saved.Document; savedHash = saved.Hash; Dirty = false;
            RenderDraft();
        }

        private void RefreshApplyStatus()
        {
            if (!readable) return;
            if (Dirty) { status.Text = "有未保存的修改。点击保存后，新版 FDU 才会加载。"; status.ForeColor = Color.DarkOrange; return; }
            var applied = store.GetApplyStatus(savedHash);
            status.Text = applied.Message;
            status.ForeColor = applied.Applied ? Color.DarkGreen : SystemColors.ControlText;
        }
    }
}
