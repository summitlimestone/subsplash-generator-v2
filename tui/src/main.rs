use slt::{Border, Color, Context, RunConfig, TableState, Theme};
use std::time::Duration;

fn main() -> std::io::Result<()> {
    let mut processes = TableState::new(
        vec!["JOBS", "SERIES", "STATUS"],
        vec![vec!["api-gateway", "2458", "22.9", "11.3"],
            vec!["worker-pool", "3112", "34.6", "9.8"],
            vec!["postgres", "1421", "12.4", "18.2"],
            vec!["otel-agent", "6180", "8.1", "2.7"],
            vec!["backup-sync", "9211", "2.7", "1.5"],
            vec!["log-shipper", "7332", "5.2", "3.1"]],
    );

    slt::run_with(
        RunConfig::default()
            .theme(Theme::dark())
            .mouse(true)
            .tick_rate(Duration::from_millis(16)),
        |ui: &mut Context| {
            if ui.key('q') { ui.quit(); }

            ui.container().gap(1).grow(1).col(|ui| {
                ui.bordered(Border::Rounded).px(2).gap(1).row(|ui| {
                    ui.text("SUBSPLASH TUI").bold().fg(Color::Green);
                    ui.badge("Recording");
                    ui.spacer();
                    ui.text("Series:").dim();
                });
                ui.container().gap(1).row(|ui| {
                    ui.stat("CPU Usage", "73%");
                    ui.stat("RAM", "8.2 / 16 GB");
                    ui.stat("Disk", "234 / 512 GB");
                    ui.stat("Network", "1.2 Gbps");
                });
                ui.container().gap(1).row(|ui| {
                    ui.bordered(Border::Single).pad(1).gap(1).grow(1).col(|ui| {
                        ui.divider_text("Trim Progress");
                        ui.sparkline(&[45.0, 52.0, 59.0, 63.0, 58.0, 66.0, 71.0, 69.0, 74.0, 78.0, 73.0, 70.0], 30);
                        ui.progress_bar(0.0, 30);
                    });
                    ui.bordered(Border::Single).pad(1).gap(1).grow(1).col(|ui| {
                        ui.divider_text("Stitch Progress");
                        ui.sparkline(&[680.0, 702.0, 744.0, 719.0, 765.0, 798.0, 812.0, 805.0, 836.0, 861.0, 847.0, 834.0], 30);
                    });
                });
                let _ = ui.table(&mut processes);
                let _ = ui.help(&[("q", "quit"), ("Tab", "focus"), ("Enter", "select"), ("r", "refresh"), ("?", "help")]);
            });
        },
    )
}
