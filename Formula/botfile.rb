# frozen_string_literal: true

class Botfile < Formula
  desc "Symlink farm for AI agent skills and instructions"
  homepage "https://codeberg.org/botfile/botfile"
  url "https://codeberg.org/botfile/botfile/archive/v0.2.0.tar.gz"
  sha256 "902ac2a595b86cd85ac62d9bd7296256ee0412da7cb006774e87dbfbcae5a2a4"
  license "Apache-2.0"

  head "https://codeberg.org/botfile/botfile.git", branch: "main"

  livecheck do
    url :stable
    regex(/^v?(\d+(?:\.\d+)+)$/i)
  end

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w -X main.version=#{version}"), "./cmd/botfile"
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/botfile version")
    assert_match "botfile", shell_output("#{bin}/botfile guide")
    assert_match(/failed/, shell_output("#{bin}/botfile plan", 2))
  end
end
