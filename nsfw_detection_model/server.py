from http.server import HTTPServer, BaseHTTPRequestHandler
import json
import base64
import io
import torch
from PIL import Image
from transformers import AutoModelForImageClassification, ViTImageProcessor
import torch.nn.functional as F

# Global variables to store model and processor
model = None
processor = None

def load_model(rec=False):
    """Load the model and processor once when server starts"""
    global model, processor

    try:
        model = AutoModelForImageClassification.from_pretrained(
            "./models/nsfw_detection/",
            local_files_only=True
        )
        processor = ViTImageProcessor.from_pretrained(
            "./models/nsfw_detection/",
            local_files_only=True
        )
        print("Model loaded successfully!")
    except Exception as e:
        print(f"Error loading model: {e}")
        model = AutoModelForImageClassification.from_pretrained("Falconsai/nsfw_image_detection")
        model.save_pretrained("/app/models/nsfw_detection/")
        processor = ViTImageProcessor.from_pretrained('Falconsai/nsfw_image_detection')
        processor.save_pretrained("/app/models/nsfw_detection/")
        print("Model downloaded and loaded successfully!")
        if not rec:
            load_model(rec=True)


def predict_nsfw(image):
    """
    Predict if image is NSFW from PIL Image object
    Returns: (is_nsfw: bool, certainty: float)
    """
    try:
        with torch.no_grad():
            inputs = processor(images=image, return_tensors="pt")
            outputs = model(**inputs)
            logits = outputs.logits

            # Convert logits to probabilities using softmax
            probabilities = F.softmax(logits, dim=-1)

            # Get the predicted class and its probability
            predicted_class_id = logits.argmax(-1).item()
            certainty = probabilities[0][predicted_class_id].item()

            # Get the label name
            predicted_label = model.config.id2label[predicted_class_id]
            is_nsfw = predicted_label.lower() == 'nsfw'

            return is_nsfw, certainty

    except Exception as e:
        raise Exception(f"Error processing image: {str(e)}")


class NSFWHandler(BaseHTTPRequestHandler):
    def _send_response(self, status_code, data, content_type='application/json'):
        """Helper method to send JSON responses"""
        self.send_response(status_code)
        self.send_header('Content-type', content_type)
        self.send_header('Access-Control-Allow-Origin', '*')  # Enable CORS
        self.end_headers()

        if isinstance(data, dict):
            response_data = json.dumps(data).encode('utf-8')
        else:
            response_data = data.encode('utf-8')

        self.wfile.write(response_data)

    def _send_error(self, status_code, message):
        """Helper method to send error responses"""
        error_response = {'error': message}
        self._send_response(status_code, error_response)

    def do_POST(self):
        """Handle POST requests"""
        if self.path == '/v1/image_nsfw':
            self.handle_nsfw_check()
        else:
            self._send_error(404, 'Endpoint not found')

    def do_GET(self):
        """Handle GET requests"""
        if self.path == '/health':
            self.handle_health_check()
        else:
            self._send_error(404, 'Endpoint not found')

    def do_OPTIONS(self):
        """Handle OPTIONS requests for CORS"""
        self.send_response(200)
        self.send_header('Access-Control-Allow-Origin', '*')
        self.send_header('Access-Control-Allow-Methods', 'GET, POST, OPTIONS')
        self.send_header('Access-Control-Allow-Headers', 'Content-Type')
        self.end_headers()

    def handle_nsfw_check(self):
        """Handle NSFW detection requests with base64 image data"""
        try:
            # Get content length
            content_length = int(self.headers.get('Content-Length', 0))

            if content_length == 0:
                self._send_error(400, 'No request body provided')
                return

            # Read and parse JSON data
            post_data = self.rfile.read(content_length)

            try:
                data = json.loads(post_data.decode('utf-8'))
            except json.JSONDecodeError:
                self._send_error(400, 'Invalid JSON in request body')
                return

            # Validate required fields
            if not isinstance(data, dict) or 'image_data' not in data:
                self._send_error(400, 'Missing "image_data" in request body. Expected base64 encoded image.')
                return

            try:
                # Get base64 image data
                image_data = data['image_data']

                # Remove data URL prefix if present (e.g., "data:image/jpeg;base64,")
                if ',' in image_data and image_data.startswith('data:'):
                    image_data = image_data.split(',')[1]

                # Decode base64
                image_bytes = base64.b64decode(image_data)

                # Open image from bytes
                img = Image.open(io.BytesIO(image_bytes))

                # Convert to RGB if necessary (handles RGBA, grayscale, etc.)
                if img.mode != 'RGB':
                    img = img.convert('RGB')

            except base64.binascii.Error:
                self._send_error(400, 'Invalid base64 encoding')
                return
            except Exception as e:
                self._send_error(400, f'Invalid image data: {str(e)}')
                return

            # Predict NSFW
            is_nsfw, certainty = predict_nsfw(img)

            response = {
                'nsfw': is_nsfw,
                'certainty': round(certainty, 4)
            }

            self._send_response(200, response)

        except Exception as e:
            self._send_error(500, f'Internal server error: {str(e)}')

    def handle_health_check(self):
        """Handle health check requests"""
        response = {
            'status': 'healthy',
            'model_loaded': model is not None,
            'supported_formats': ['base64']
        }
        self._send_response(200, response)

    def log_message(self, format, *args):
        """Override to customize logging"""
        print(f"[{self.date_time_string()}] {format % args}")


def run_server(host='localhost', port=6969):
    """Run the HTTP server"""
    server_address = (host, port)
    httpd = HTTPServer(server_address, NSFWHandler)

    print(f"Server running on http://{host}:{port}")
    print(f"NSFW endpoint: http://{host}:{port}/v1/image_nsfw")
    print(f"Health check: http://{host}:{port}/health")
    print("Supported format: Base64 encoded images only")
    print("Press Ctrl+C to stop the server")

    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        print("\nShutting down server...")
        httpd.shutdown()


if __name__ == '__main__':
    load_model()
    run_server(host='0.0.0.0', port=6969)
