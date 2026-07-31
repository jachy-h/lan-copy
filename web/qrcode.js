((global) => {
	// QR Code Model 2, error-correction level L. Versions 1–5 use one
	// Reed–Solomon block, which is enough for local network URLs.
	const versions = [
		null,
		{ dataCodewords: 19, errorCodewords: 7, alignment: [] },
		{ dataCodewords: 34, errorCodewords: 10, alignment: [6, 18] },
		{ dataCodewords: 55, errorCodewords: 15, alignment: [6, 22] },
		{ dataCodewords: 80, errorCodewords: 20, alignment: [6, 26] },
		{ dataCodewords: 108, errorCodewords: 26, alignment: [6, 30] },
	];

	const exponent = new Uint8Array(512);
	const logarithm = new Uint8Array(256);
	let value = 1;
	for (let index = 0; index < 255; index += 1) {
		exponent[index] = value;
		logarithm[value] = index;
		value <<= 1;
		if (value & 0x100) value ^= 0x11d;
	}
	for (let index = 255; index < exponent.length; index += 1) {
		exponent[index] = exponent[index - 255];
	}

	function multiply(left, right) {
		return left && right ? exponent[logarithm[left] + logarithm[right]] : 0;
	}

	function makeDivisor(degree) {
		const result = new Uint8Array(degree);
		result[degree - 1] = 1;
		let root = 1;
		for (let index = 0; index < degree; index += 1) {
			for (let term = 0; term < degree; term += 1) {
				result[term] = multiply(result[term], root);
				if (term + 1 < degree) result[term] ^= result[term + 1];
			}
			root = multiply(root, 2);
		}
		return result;
	}

	function makeRemainder(data, divisor) {
		const result = new Uint8Array(divisor.length);
		for (const byte of data) {
			const factor = byte ^ result[0];
			result.copyWithin(0, 1);
			result[result.length - 1] = 0;
			for (let index = 0; index < result.length; index += 1) {
				result[index] ^= multiply(divisor[index], factor);
			}
		}
		return result;
	}

	function appendBits(bits, number, length) {
		for (let index = length - 1; index >= 0; index -= 1) {
			bits.push((number >>> index) & 1);
		}
	}

	function makeCodewords(bytes, settings) {
		const capacity = settings.dataCodewords * 8;
		const bits = [];
		appendBits(bits, 0b0100, 4); // Byte mode.
		appendBits(bits, bytes.length, 8);
		for (const byte of bytes) appendBits(bits, byte, 8);
		appendBits(bits, 0, Math.min(4, capacity - bits.length));
		while (bits.length % 8) bits.push(0);

		const data = [];
		for (let offset = 0; offset < bits.length; offset += 8) {
			let byte = 0;
			for (let index = 0; index < 8; index += 1) {
				byte = (byte << 1) | bits[offset + index];
			}
			data.push(byte);
		}
		for (let pad = 0; data.length < settings.dataCodewords; pad += 1) {
			data.push(pad % 2 ? 0x11 : 0xec);
		}

		const remainder = makeRemainder(data, makeDivisor(settings.errorCodewords));
		return [...data, ...remainder];
	}

	function formatBits(mask) {
		const data = (0b01 << 3) | mask; // Error-correction level L.
		let remainder = data;
		for (let index = 0; index < 10; index += 1) {
			remainder = (remainder << 1) ^ (((remainder >>> 9) & 1) * 0x537);
		}
		return ((data << 10) | remainder) ^ 0x5412;
	}

	function makeMatrix(text) {
		const bytes = new TextEncoder().encode(text);
		const version = versions.findIndex(
			(settings, index) =>
				index > 0 && 4 + 8 + bytes.length * 8 <= settings.dataCodewords * 8,
		);
		if (version < 1) throw new Error("二维码地址过长");

		const settings = versions[version];
		const size = version * 4 + 17;
		const modules = Array.from({ length: size }, () => Array(size).fill(false));
		const reserved = Array.from({ length: size }, () =>
			Array(size).fill(false),
		);
		const setFunction = (row, column, dark) => {
			if (row < 0 || column < 0 || row >= size || column >= size) return;
			modules[row][column] = Boolean(dark);
			reserved[row][column] = true;
		};

		function drawFinder(top, left) {
			for (let row = -1; row <= 7; row += 1) {
				for (let column = -1; column <= 7; column += 1) {
					const inside = row >= 0 && row <= 6 && column >= 0 && column <= 6;
					const dark =
						inside &&
						(row === 0 ||
							row === 6 ||
							column === 0 ||
							column === 6 ||
							(row >= 2 && row <= 4 && column >= 2 && column <= 4));
					setFunction(top + row, left + column, dark);
				}
			}
		}

		drawFinder(0, 0);
		drawFinder(0, size - 7);
		drawFinder(size - 7, 0);
		for (let index = 8; index < size - 8; index += 1) {
			setFunction(6, index, index % 2 === 0);
			setFunction(index, 6, index % 2 === 0);
		}
		for (const row of settings.alignment) {
			for (const column of settings.alignment) {
				if (reserved[row][column]) continue;
				for (let deltaRow = -2; deltaRow <= 2; deltaRow += 1) {
					for (let deltaColumn = -2; deltaColumn <= 2; deltaColumn += 1) {
						const distance = Math.max(
							Math.abs(deltaRow),
							Math.abs(deltaColumn),
						);
						setFunction(
							row + deltaRow,
							column + deltaColumn,
							distance === 2 || distance === 0,
						);
					}
				}
			}
		}

		const mask = 0;
		const format = formatBits(mask);
		const getFormatBit = (index) => (format >>> index) & 1;
		for (let index = 0; index <= 5; index += 1) {
			setFunction(index, 8, getFormatBit(index));
		}
		setFunction(7, 8, getFormatBit(6));
		setFunction(8, 8, getFormatBit(7));
		setFunction(8, 7, getFormatBit(8));
		for (let index = 9; index < 15; index += 1) {
			setFunction(8, 14 - index, getFormatBit(index));
		}
		for (let index = 0; index < 8; index += 1) {
			setFunction(8, size - 1 - index, getFormatBit(index));
		}
		for (let index = 8; index < 15; index += 1) {
			setFunction(size - 15 + index, 8, getFormatBit(index));
		}
		setFunction(size - 8, 8, true);

		const codewords = makeCodewords(bytes, settings);
		let bitIndex = 0;
		for (let right = size - 1; right >= 1; right -= 2) {
			if (right === 6) right = 5;
			const upward = ((right + 1) & 2) === 0;
			for (let vertical = 0; vertical < size; vertical += 1) {
				const row = upward ? size - 1 - vertical : vertical;
				for (let offset = 0; offset < 2; offset += 1) {
					const column = right - offset;
					if (reserved[row][column]) continue;
					const dark =
						bitIndex < codewords.length * 8 &&
						((codewords[bitIndex >>> 3] >>> (7 - (bitIndex & 7))) & 1) !== 0;
					modules[row][column] = dark !== ((row + column) % 2 === 0);
					bitIndex += 1;
				}
			}
		}
		return modules;
	}

	function createSVG(text) {
		const modules = makeMatrix(text);
		const quietZone = 4;
		const size = modules.length + quietZone * 2;
		const namespace = "http://www.w3.org/2000/svg";
		const svg = document.createElementNS(namespace, "svg");
		svg.setAttribute("viewBox", `0 0 ${size} ${size}`);
		svg.setAttribute("shape-rendering", "crispEdges");
		svg.setAttribute("aria-hidden", "true");

		const background = document.createElementNS(namespace, "rect");
		background.setAttribute("width", String(size));
		background.setAttribute("height", String(size));
		background.setAttribute("fill", "#fff");
		svg.append(background);

		const path = document.createElementNS(namespace, "path");
		const commands = [];
		for (let row = 0; row < modules.length; row += 1) {
			for (let column = 0; column < modules.length; column += 1) {
				if (modules[row][column]) {
					commands.push(`M${column + quietZone} ${row + quietZone}h1v1h-1z`);
				}
			}
		}
		path.setAttribute("d", commands.join(""));
		path.setAttribute("fill", "#14213d");
		svg.append(path);
		return svg;
	}

	global.QRCode = { createSVG, matrix: makeMatrix };
})(globalThis);
